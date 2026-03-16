# Cliros
<img width="1384" height="1040" alt="Untitled design (1)" src="https://github.com/user-attachments/assets/c4b3ceaa-d520-4f76-aeef-157d4c17ec7c" />

Go-библиотека для реализации паттерна **Choreography Saga** в распределённых системах с гарантированной доставкой сообщений через **Outbox/Inbox**.

Библиотека подключается к каждому сервису отдельно. Центрального координатора нет — сервисы общаются через брокер сообщений, каждый самостоятельно реагирует на входящие события.

## Зачем это нужно

В микросервисной архитектуре часто нужно выполнить несколько операций в разных сервисах как единую логическую транзакцию: создать заказ, списать оплату, зарезервировать товар на складе. Если один из сервисов упал на середине — нужно откатить уже сделанное.

Классические распределённые транзакции (2PC) сложны в реализации и создают проблемы с доступностью. Паттерн **Saga** решает эту задачу иначе: каждый сервис выполняет свою часть и публикует событие; при ошибке запускается цепочка компенсирующих действий.

Существует два подхода к реализации саг:

- **Orchestration** — есть центральный координатор, который знает весь сценарий и явно управляет каждым сервисом.
- **Choreography** — центрального координатора нет. Каждый сервис знает только свою роль: на какой топик подписаться, что сделать при успехе, куда отправить сообщение дальше и как компенсировать своё действие при ошибке. Сервисы взаимодействуют напрямую через брокер.

**Эта библиотека реализует choreography.** Каждый сервис разворачивает свой экземпляр `Controller`, регистрирует собственные шаги и самостоятельно реагирует на входящие события. Сервисы не знают друг о друге — только о топиках.

> **Когда использовать Kliros.** Хореография хорошо работает при небольшом числе участников — до 4–5 сервисов. При большем количестве сервисов цепочки событий становятся трудно отслеживаемыми, а логика компенсаций распределяется по всей системе, что усложняет отладку и понимание сценариев. В таких случаях принято рекомендовать **orchestration** — подход с центральным координатором, который явно управляет всей последовательностью шагов.

Библиотека даёт готовую инфраструктуру для обоих паттернов: вы пишете только бизнес-логику шагов, остальное берёт на себя библиотека.

## Возможности

- **Хореография саги**: библиотека разворачивается на каждом сервисе отдельно. Вы описываете шаги (`Execute` + `Compensate`) и маршрутизацию (топики), сервисы реагируют на события автономно без центрального координатора.
- **Outbox/Inbox**: атомарная запись сообщений в БД и фоновая публикация в брокер; защита от повторной обработки через inbox.
- **Retry и Backoff**: настраиваемые политики повтора для бизнес-логики и инфраструктурных операций; exponential backoff с опциональным jitter.
- **Брокеры**: абстракция `broker.Publisher` / `broker.Subscriber` — подключите любой брокер. Готовые примеры для Kafka и NATS.
- **БД**: поддержка PostgreSQL и MySQL через стандартный `database/sql`; миграции outbox/inbox-таблиц применяются автоматически.
- **Метрики**: опциональная интеграция с Prometheus.
- **Трейсинг**: опциональная интеграция с OpenTelemetry; trace-контекст передаётся в сообщениях между сервисами.

## Установка

```bash
go get github.com/SosisterRapStar/Cliros
```

Требуется Go 1.21+.

## Быстрый старт

Полный рабочий пример: [`examples/order-saga/main.go`](examples/order-saga/main.go).

### 1. Подключение к БД

```go
import (
    "database/sql"
    _ "github.com/jackc/pgx/v5/stdlib"
    "github.com/SosisterRapStar/Cliros/database"
)

db, err := sql.Open("pgx", "postgres://user:pass@localhost:5432/mydb?sslmode=disable")
if err != nil { /* ... */ }

dbCtx := database.NewDBContext(db, database.SQLDialectPostgres)
// Для MySQL: database.SQLDialectMySQL
```

### 2. Реализация брокера

Реализуйте интерфейсы `broker.Publisher` и `broker.Subscriber` для вашего брокера, или используйте готовый адаптер из `pubsub/nats` / `examples/kafka-pubsub`.

```go
// Минимальный интерфейс:
type Publisher interface {
    Publish(ctx context.Context, topic string, message message.Message) error
}

type Subscriber interface {
    Subscribe(ctx context.Context, topic string, handler func(ctx context.Context, message message.Message) error) error
    Close() error
}
```

### 3. Создание контроллера

```go
import (
    "github.com/SosisterRapStar/Cliros/controller"
    "github.com/SosisterRapStar/Cliros/retry"
    "github.com/SosisterRapStar/Cliros/backoff"
)

errCh := make(chan error, 128)

ctrl, err := controller.New(&controller.Config{
    Subscriber: myBroker,
    Publisher:  myBroker,
    DB:         dbCtx,

    // Retry для инфраструктурных операций (BeginTx, Commit, WriteOutbox).
    // Если nil — без повторов.
    InfraRetry: &retry.Retrier{
        BackoffOptions: retry.BackoffOptions{
            BackoffPolicy: backoff.Expontential{},
            MinBackoff:    50 * time.Millisecond,
            MaxBackoff:    5 * time.Second,
        },
        MaxRetries: 10,
    },

    PollInterval: 1 * time.Second,  // интервал опроса outbox (default: 1s)
    BatchSize:    10,                // размер батча при чтении outbox (default: 10)

    // Backoff для повторной публикации из outbox при ошибках брокера
    BackoffPolicy: backoff.Expontential{},
    BackoffMin:    100 * time.Millisecond,
    BackoffMax:    1 * time.Minute,

    ErrCh: errCh, // канал ошибок Reader'а; если nil — ошибки молча теряются
})
if err != nil { /* ... */ }
```

### 4. Описание шага

Шаг содержит бизнес-логику (`Execute`), компенсацию (`Compensate`) и маршрутизацию. Внутри `Execute` и `Compensate` вы получаете готовую транзакцию `tx` — в ней же работает outbox, так что всё атомарно.

```go
import (
    "github.com/SosisterRapStar/Cliros/step"
    "github.com/SosisterRapStar/Cliros/database"
    "github.com/SosisterRapStar/Cliros/message"
)

orderStep, err := step.New(&step.StepParams{
    Name: "create-order",

    Execute: func(ctx context.Context, tx database.TxQueryer, msg message.Message) (message.Message, error) {
        // Ваша бизнес-логика выполняется в той же транзакции, что и запись в outbox.
        _, err := tx.ExecContext(ctx,
            "INSERT INTO orders (id, status) VALUES ($1, $2)",
            "order-123", "created",
        )
        if err != nil {
            return message.Message{}, fmt.Errorf("insert order: %w", err)
        }

        // Возвращаем сообщение для следующего шага
        return message.Message{
            MessagePayload: message.MessagePayload{
                Payload: map[string]any{"order_id": "order-123"},
            },
        }, nil
    },

    Compensate: func(ctx context.Context, tx database.TxQueryer, msg message.Message) (message.Message, error) {
        _, err := tx.ExecContext(ctx,
            "UPDATE orders SET status = $1 WHERE id = $2",
            "cancelled", msg.Payload["order_id"],
        )
        if err != nil {
            return message.Message{}, fmt.Errorf("cancel order: %w", err)
        }
        return msg, nil
    },

    Routing: step.RoutingConfig{
        NextStepTopics: []string{"payment-service.process"},   // топик для следующего шага
        ErrorTopics:    []string{"order-service.compensate"},  // топик при ошибке
    },

    // Опционально: обработчик ошибок после исчерпания всех retry
    OnError: func(ctx context.Context, tx database.TxQueryer, msg message.Message, err error) (message.Message, error) {
        log.Printf("step failed: %v", err)
        return msg, err // вернуть ошибку → отправить failed-событие в ErrorTopics
    },

    // Опционально: retry для бизнес-логики шага
    // RetryPolicy: &retry.Retrier{...},
})
if err != nil { /* ... */ }
```

### 5. Регистрация и запуск

Каждый сервис регистрирует только свои шаги и подписывается только на свои топики. Другие сервисы делают то же самое у себя — каждый со своим `Controller`.

```go
// Подписываем шаг на топик — при получении сообщения вызывается Execute или Compensate
if err := ctrl.Register("order-service.create", orderStep); err != nil { /* ... */ }

// Init: применяет миграции БД (идемпотентно) и запускает фоновый Reader
if err := ctrl.Init(ctx); err != nil { /* ... */ }
defer ctrl.Close()

// Слушаем ошибки Reader'а
go func() {
    for err := range errCh {
        log.Printf("reader error: %v", err)
    }
}()

// Запускаем сагу
if err := ctrl.StartSaga(ctx, orderStep, message.Message{}); err != nil { /* ... */ }
```

## Несколько шагов в одном процессе

Если нужно последовательно выполнить несколько шагов без участия брокера (например, при инициализации саги):

```go
steps := []*step.Step{stepA, stepB, stepC}
resultMsg, err := ctrl.StartSagaWithSteps(ctx, steps, initialMsg)
```

Результат каждого шага передаётся на вход следующему. При ошибке любого шага выполнение прерывается.

## Retry в бизнес-логике

Если ошибка из `Execute` или `Compensate` — транзиентная (сеть, внешний API), оберните её в `retry.AsRetryable`:

```go
Execute: func(ctx context.Context, tx database.TxQueryer, msg message.Message) (message.Message, error) {
    resp, err := externalAPI.Call(ctx)
    if err != nil {
        return message.Message{}, retry.AsRetryable(err) // будет повторено согласно RetryPolicy
    }
    // ...
},
```

Не-retryable ошибки сразу передаются в `OnError`.

## Метрики и трейсинг

### Prometheus

```go
import "github.com/prometheus/client_golang/prometheus"

registry := prometheus.NewRegistry()
m, err := controller.NewMetrics(registry, "saga")
if err != nil { /* ... */ }

ctrl, err := controller.New(&controller.Config{
    // ...
    Metrics: m,
})
```

### OpenTelemetry

```go
ctrl, err := controller.New(&controller.Config{
    // ...
    Tracing: &controller.TracingConfig{
        TracerName: "order-service", // отображается как instrumentation scope в вашем бэкенде
        // Tracer: myTracer, // опционально, если нужен свой TracerProvider
    },
})
```

Trace-контекст автоматически инжектируется в сообщения между сервисами и извлекается при получении.

## Структура пакетов

| Пакет | Описание |
|---|---|
| `controller` | Точка входа: `New`, `Register`, `Init`, `StartSaga` |
| `step` | Описание шагов саги: `Step`, `StepParams`, `RoutingConfig` |
| `message` | Тип сообщения, передаваемого между шагами |
| `broker` | Интерфейсы `Publisher` / `Subscriber` |
| `database` | Обёртка над `database/sql`: `DBContext`, `TxQueryer` |
| `retry` | Политика retry: `Retrier`, `AsRetryable` |
| `backoff` | Политики backoff: `Exponential`, `ExponentialWithJitter` |
| `pubsub/nats` | Готовый адаптер для NATS |
| `adapter/pgx` | Готовый адаптер для pgx |

## Примеры

- [`examples/order-saga`](examples/order-saga/main.go) — сага создания заказа с PostgreSQL
- [`examples/kafka-pubsub`](examples/kafka-pubsub/kafka.go) — адаптер для Kafka
- [`pubsub/nats`](pubsub/nats/nats.go) — адаптер для NATS
