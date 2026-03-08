# План реализации наблюдаемости (метрики + трейсинг)

## Исходные решения

- **Локальное хранилище саг не делаем** — пользователь не получает абстракцию для сохранения состояния шагов в БД из коробки.
- **Метрики**: пользователь подключает **свой Prometheus** (свой scrape / свой Prometheus Server). Фреймворк лишь **собирает базовые метрики по саге из коробки** и даёт возможность зарегистрировать их в переданном `prometheus.Registry`.
- **Трейсинг**: фреймворк **не отвечает за инфраструктуру трейсинга** (экспортеры, sampler, TracerProvider). Ответственность фреймворка:
  1. **Передача контекста** — проброс trace-контекста по цепочке (в процессе и между шагами через сообщения).
  2. **Сбор трейсов с этапов** — создание спэнов для выполнения/компенсации шагов и привязка к существующему trace из контекста/сообщения.

---

## 1. Метрики (Prometheus)

### 1.1 Идея

- Зависимость: `github.com/prometheus/client_golang` (опциональная: только если пользователь хочет метрики).
- Пользователь передаёт в конфиг **свой** `*prometheus.Registry` (или фреймворк создаёт отдельный registry, который пользователь добавляет в свой `promhttp.Handler`).
- Метрики **регистрируются при создании Controller/Executor** и обновляются в executor при выполнении шагов.

### 1.2 Набор метрик «из коробки»

| Метрика | Тип | Labels | Описание |
|--------|-----|--------|----------|
| `saga_steps_total` | Counter | `step`, `operation` (`execute` / `compensate`), `status` (`success` / `failure`) | Количество выполненных шагов по результату |
| `saga_step_duration_seconds` | Histogram | `step`, `operation` | Длительность выполнения шага (execute/compensate) |
| `saga_started_total` | Counter | — | Количество запущенных саг (вызовы `StartSaga` / `StartSagaWithSteps`) |
| `saga_completed_total` | Counter | `status` (`success` / `failure`) | Завершённые саги (для синхронного пути `StartSagaWithSteps`; опционально) |

- Все метрики под префиксом, например `saga_`, чтобы не конфликтовать с остальными метриками приложения.
- Имена и набор лейблов можно уточнить (например, добавить `saga_id` только в exemplars, а не в labels, чтобы не раздувать кардинальность).

### 1.3 Точки интеграции

- **Controller**: при создании принимает опциональный `MetricsConfig` (или `Registry` + опции). Если передан — создаётся объект метрик (например, `saga/metrics.Metrics`), который передаётся в executor.
- **Executor**: при вызове `ExecuteStep` / `CompensateStep`:
  - перед действием: запомнить `time.Now()`;
  - после: инкремент `saga_steps_total{step, operation, status}`, наблюдение `saga_step_duration_seconds`.
- **Controller**: в `StartSaga` / `StartSagaWithSteps` при старте инкремент `saga_started_total`; при завершении `StartSagaWithSteps` (успех/ошибка) — `saga_completed_total`.

### 1.4 Интерфейс для пользователя

- Вариант A: в `controller.Config` поле `PrometheusRegistry *prometheus.Registry`. Если не nil — при `New` регистрируем метрики в этом registry. Пользователь сам поднимает HTTP с `promhttp.HandlerFor(cfg.PrometheusRegistry, …)` и настраивает scrape у себя.
- Вариант B: отдельный пакет `saga/prometheus` с функцией `Register(registry *prometheus.Registry, namespace string) (*Metrics, error)`, возвращающий объект с методами типа `ObserveStepExecute(step string, duration time.Duration, err error)`. Тогда Controller/Executor принимают опциональный `*Metrics` и вызывают эти методы. Регистрация в registry делается пользователем через `prometheus.Register(...)` или через наш `Register`.

Рекомендация: **Вариант B** — пакет `observability/metrics` (или `saga/metrics`) с типом `Metrics` и `Register(registry, subsystem)`, чтобы не тянуть prometheus в ядро controller. В `controller.Config` опциональное поле `Metrics *metrics.Metrics`.

### 1.5 Конкретные шаги реализации (метрики)

1. Добавить пакет `internal/observability/metrics` (или `metrics` в корне, если не хочется папки observability).
2. Определить структуру `Metrics` с полями: `StepsTotal`, `StepDuration`, `SagaStarted`, `SagaCompleted` (все типы `*prometheus.CounterVec` / `*prometheus.HistogramVec`).
3. Функция `New(registry *prometheus.Registry, subsystem string) (*Metrics, error)` создаёт и регистрирует метрики в переданном registry.
4. В `executor`: добавить опциональное поле `metrics *metrics.Metrics` в `StepExecutor` и в `New`; в `ExecuteStep`/`CompensateStep` вызывать `metrics.ObserveStep(...)` (время, step name, operation, status).
5. В `controller.Config`: поле `Metrics *metrics.Metrics`; при создании executor передавать `cfg.Metrics` в `executor.New`.
6. В Controller в `StartSaga`/`StartSagaWithSteps`: при старте саги вызывать `metrics.SagaStartedInc()`; при завершении `StartSagaWithSteps` — `metrics.SagaCompleted(status)`.
7. Документация: в README или отдельном doc — как подключить свой Prometheus (создать registry, передать в Config, выставить endpoint для scrape).

---

## 2. Трейсинг (OpenTelemetry)

### 2.1 Идея

- Фреймворк **не инициализирует** TracerProvider и не настраивает экспортеры. Пользователь в `main` делает `otel.SetTracerProvider(...)`, экспорт в Jaeger/Zipkin/OTLP — его зона ответственности.
- Фреймворк:
  - **Пробрасывает контекст** — везде по коду используется переданный `context.Context`; при вызове action/compensate передаётся тот же контекст, в котором уже может быть span (от HTTP/gRPC или от предыдущего шага).
  - **Создаёт спэны для этапов** — один span на выполнение шага (execute или compensate) с атрибутами `saga.id`, `step.name`, `step.operation` (execute/compensate), статус (ok/error).
  - **Передача контекста между шагами через сообщения** — при асинхронной доставке следующий consumer должен продолжить тот же trace. Для этого нужно сохранять в сообщении trace context (W3C Trace Context: traceparent, tracestate) и при обработке входящего сообщения восстанавливать контекст и создавать дочерний span.

### 2.2 Точки создания спэнов

- **Executor**:
  - В начале `ExecuteStep`: `ctx, span := tracer.Start(ctx, "saga.step.execute", ...)`, атрибуты: `saga.id`, `step.name`; в конце `span.End()`, при ошибке `span.RecordError(err)`, `span.SetStatus(codes.Error, ...)`.
  - Аналогично в `CompensateStep`: имя спэна `saga.step.compensate`.
- **Controller**:
  - В `StartSaga` / `StartSagaWithSteps`: опционально один span `saga.start` с атрибутом `saga.id` (если нужен общий span на всю сагу при синхронном запуске).

Все вызовы action/compensate идут с `ctx`, в котором уже есть текущий span — пользовательский код внутри шага может создавать дочерние спэны.

### 2.3 Передача trace context через сообщения (async)

- Чтобы при переходе к следующему шагу по брокеру трейс не обрывался, в исходящем сообщении нужно нести trace context.
- Варианты:
  - Добавить в `MessageMeta` (или в расширяемую структуру) поля для trace: например `Traceparent`, `Tracestate` (W3C) или один blob `TraceContext`.
  - При записи в outbox (в executor перед `WriteMessages`) вызывать `propagation.Inject(ctx, carrier)` и сохранять результат в сообщение (например в метаданные или в payload под зарезервированными ключами).
  - При обработке входящего сообщения (в handler, который вызывает `ExecuteStep`/`CompensateStep`) в самом начале вызвать `propagation.Extract(carrier) -> ctx` и дальше использовать этот `ctx` для вызова executor. Тогда span, созданный в executor, будет дочерним к span из входящего trace.

Конкретизация:

- В **message**: добавить опциональные поля, например `Traceparent string`, `Tracestate string` (или один `TraceContext map[string]string`). Сериализация в JSON/брокер должна их включать.
- **Inject**: в executor перед записью в outbox (в `atomicRun` и в `runErrorHandler`, где вызывается `WriteMessages`) — взять текущий `ctx`, вызвать `otel.GetTextMapPropagator().Inject(ctx, ...)` в carrier, реализующий interface для записи в `Message` (Traceparent/Tracestate).
- **Extract**: в controller в callback подписки (в `Register`) в начале: построить carrier из `msg`, вызвать `propagation.Extract(carrier) -> ctx`, заменить контекст вызова на этот (или объединить с существующим). Затем вызвать `ExecuteStep(ctx, ...)` / `CompensateStep(ctx, ...)`.

Зависимость: `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/trace`, `go.opentelemetry.io/otel/propagation`, `go.opentelemetry.io/otel/codes`. Трейсинг тоже опциональный: если в конфиге передан TracerProvider или глобальный уже установлен — используем; иначе работаем без спэнов (только передача контекста, если поля сообщения заполнены).

### 2.4 Конкретные шаги реализации (трейсинг)

1. Добавить в `message.MessageMeta` поля `Traceparent string` и `Tracestate string` (или общий `TraceContext map[string]string` для совместимости с `propagation.TextMapCarrier`). Убедиться, что NATS/outbox сериализуют их.
2. Пакет `internal/observability/tracing` (или `tracing`): функция `StartStepSpan(ctx, sagaID, stepName, operation string) (context.Context, trace.Span)`; обёртка для inject/extract через message.
3. Carrier для message: тип, реализующий `propagation.TextMapCarrier`, читающий/пишущий `Traceparent`/`Tracestate` в/из `Message`.
4. Executor: в `ExecuteStep`/`CompensateStep` в начале создавать span через этот пакет, передавать обновлённый `ctx` во все внутренние вызовы; перед `WriteMessages` вызывать inject в текущий `msg` из `ctx`; в конце End/RecordError/SetStatus.
5. Controller: в handler подписки в начале извлечь из `msg` trace context в `ctx`; вызывать executor с этим `ctx`. В `StartSaga`/`StartSagaWithSteps` опционально создавать span `saga.start`.
6. Документация: как включить трейсинг (установить TracerProvider в main), как передаётся контекст и что фреймворк не настраивает экспорт.

---

## 3. Зависимости и опциональность

- **Prometheus**: добавить `github.com/prometheus/client_golang` в `go.mod`. Использовать только в пакете метрик и в том коде, куда передаётся `*Metrics`. Если пользователь не передаёт `Metrics`, зависимость не используется в рантайме (но остаётся в модуле).
- **OpenTelemetry**: добавить `go.opentelemetry.io/otel`, `otel/trace`, `otel/propagation`, `otel/codes`. Проверять при создании спэна: если `trace.SpanFromContext(ctx).SpanContext().IsValid()` или глобальный TracerProvider не noop — создаём спэны и делаем inject; иначе только пробрасываем ctx и (если в сообщении есть trace) делаем extract.

---

## 4. Обновление ANALYSIS.md (п. 6)

В разделе «Встроенная наблюдаемость» указать:

- Локальное хранилище состояния саг **не реализуется** по решению.
- Метрики: реализуются **базовые метрики из коробки** с возможностью подключения **своего Prometheus** (передача Registry / объекта Metrics в Config).
- Трейсинг: **автоматический трейсинг этапов** (спэны execute/compensate) и **передача trace-контекста** по цепочке и через сообщения; инфраструктура трейсинга (экспортеры, TracerProvider) — на стороне пользователя.
- OpenTelemetry: интеграция через **передачу контекста и создание спэнов**; при необходимости — хранение Traceparent/Tracestate в сообщениях для кросс-процессного трейсинга.

После реализации в «Что реализовано» добавить пункты по метрикам и трейсингу согласно этому плану.

---

## 5. План реализации (порядок работ)

Ниже — пошаговый план с фазами и зависимостями. Метрики можно реализовать первыми (не зависят от трейсинга); трейсинг разбит на фазы: сообщение → tracing-пакет → executor → controller.

### Фаза 0: Подготовка

| # | Задача | Детали |
|---|--------|--------|
| 0.1 | Добавить зависимости в `go.mod` | `go get github.com/prometheus/client_golang/prometheus`; `go get go.opentelemetry.io/otel go.opentelemetry.io/otel/trace go.opentelemetry.io/otel/propagation go.opentelemetry.io/otel/codes` |
| 0.2 | Создать структуру пакетов | Каталог `internal/observability/`; подпакеты `metrics` и `tracing` |

### Фаза 1: Метрики

| # | Задача | Файлы | Детали |
|---|--------|-------|--------|
| 1.1 | Определить тип `Metrics` и метрики | `internal/observability/metrics/metrics.go` | Поля: `StepsTotal *prometheus.CounterVec`, `StepDuration *prometheus.HistogramVec`, `SagaStarted prometheus.Counter`, `SagaCompleted *prometheus.CounterVec`. Конструкторы метрик с префиксом `saga_` и нужными labels. |
| 1.2 | Функция создания и регистрации | `internal/observability/metrics/metrics.go` | `New(registry *prometheus.Registry, subsystem string) (*Metrics, error)` — создаёт все метрики и регистрирует в `registry`. |
| 1.3 | Методы наблюдения | `internal/observability/metrics/metrics.go` | `ObserveStep(step, operation string, duration time.Duration, err error)` — инкремент StepsTotal, наблюдение StepDuration; `SagaStartedInc()`; `SagaCompleted(status string)`. Для `ObserveStep` при `metrics == nil` — no-op (проверка в вызывающем коде или внутри). |
| 1.4 | Интеграция в executor | `internal/executor/executor.go` | В `StepExecutor` поле `metrics *metrics.Metrics`; в `New` параметр и присвоение. В `ExecuteStep`/`CompensateStep`: запомнить `start := time.Now()`, после выполнения вызвать `metrics.ObserveStep(stp.Name(), "execute"|"compensate", time.Since(start), err)` (если metrics != nil). |
| 1.5 | Интеграция в controller | `controller/controller.go` | В `Config` поле `Metrics *metrics.Metrics`. При создании executor передавать `cfg.Metrics` в `executor.New`. В `StartSaga` и в начале `StartSagaWithSteps` вызывать `metrics.SagaStartedInc()`; в конце `StartSagaWithSteps` (успех/ошибка) — `metrics.SagaCompleted("success"|"failure")`. |

### Фаза 2: Трейсинг — сообщение и carrier

| # | Задача | Файлы | Детали |
|---|--------|-------|--------|
| 2.1 | Поля trace в сообщении | `message/message.go` | В `MessageMeta` добавить `Traceparent string` и `Tracestate string` (JSON-теги для сериализации). Проверить, что NATS и outbox (сериализация Message) включают эти поля. |
| 2.2 | Carrier для Message | `internal/observability/tracing/carrier.go` | Тип `MessageCarrier struct { msg *message.Message }` с методами `Get(key string) string`, `Set(key string, value string)` — для ключей `traceparent`/`tracestate` читать/писать в `msg.Traceparent`/`msg.Tracestate`. Реализовать `propagation.TextMapCarrier`. |
| 2.3 | Функции Inject/Extract для Message | `internal/observability/tracing/propagation.go` | `InjectTraceContext(ctx context.Context, msg *message.Message)` — carrier из msg, `otel.GetTextMapPropagator().Inject(ctx, carrier)`. `ExtractTraceContext(ctx context.Context, msg *message.Message) context.Context` — extract в новый ctx и возврат (или merge с входящим ctx). |

### Фаза 3: Трейсинг — спэны в executor и controller

| # | Задача | Файлы | Детали |
|---|--------|-------|--------|
| 3.1 | Пакет spans | `internal/observability/tracing/span.go` | `StartStepSpan(ctx, sagaID, stepName, operation string) (context.Context, trace.Span)` — имя спэна `saga.step.execute` или `saga.step.compensate`; атрибуты `saga.id`, `step.name`, `step.operation`. Использовать `otel.Tracer("saga").Start(...)`. Опционально: если tracer noop — возвращать ctx без span. |
| 3.2 | Executor: спэны и inject | `internal/executor/executor.go` | В начале `ExecuteStep`: `ctx, span := tracing.StartStepSpan(ctx, msg.SagaID, stp.Name(), "execute")`, defer `span.End()`, при ошибке `span.RecordError(err)`, `span.SetStatus(codes.Error, ...)`. Аналогично в `CompensateStep` с operation `"compensate"`. Перед вызовом `WriteMessages` в `atomicRun` и в `runErrorHandler`: вызвать `tracing.InjectTraceContext(ctx, &result)` для сообщения `result`, которое пишется в outbox. |
| 3.3 | Controller: extract и span саги | `controller/controller.go` | В callback в `Register`: в начале `ctx = tracing.ExtractTraceContext(ctx, &msg)` (или создать новый ctx из msg), затем вызывать `ExecuteStep(ctx, ...)` / `CompensateStep(ctx, ...)`. В `StartSaga`/`StartSagaWithSteps`: опционально создать span `saga.start` с атрибутом `saga.id`, defer End. |

### Фаза 4: Тесты и документация

| # | Задача | Файлы | Детали |
|---|--------|-------|--------|
| 4.1 | Тесты метрик | `internal/observability/metrics/metrics_test.go` | Создать registry, вызвать `New`, выполнить ObserveStep / SagaStartedInc / SagaCompleted, проверить через `prometheus.Gather()` значения счётчиков и гистограммы. |
| 4.2 | Тесты трейсинга (опционально) | `internal/observability/tracing/..._test.go` | Unit: carrier Get/Set; Extract/Inject с mock propagator. Интеграция: при желании — тест с noop tracer и проверка, что span создаётся и контекст передаётся. |
| 4.3 | Документация | README или `docs/observability.md` | Как подключить Prometheus: создать `prometheus.NewRegistry()`, вызвать `metrics.New(reg, "saga")`, передать `m` в `controller.Config.Metrics`, выставить HTTP `promhttp.HandlerFor(reg, promhttp.HandlerOpts{})`. Как включить трейсинг: в main установить `otel.SetTracerProvider(...)` и при необходимости propagator; описание передачи контекста и полей Traceparent/Tracestate в сообщениях. |

### Порядок выполнения и зависимости

```
Фаза 0 → Фаза 1 (метрики полностью)
Фаза 0 → Фаза 2 → Фаза 3 (трейсинг)
Фаза 1, 3 → Фаза 4
```

Рекомендуемая последовательность: **0 → 1 → 2 → 3 → 4**. Фазы 1 и 2 можно вести параллельно после фазы 0; фаза 3 зависит от фазы 2.
