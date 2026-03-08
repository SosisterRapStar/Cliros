package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/backoff"
	"github.com/SosisterRapStar/LETI-paper/broker"
	"github.com/SosisterRapStar/LETI-paper/database"
	"github.com/SosisterRapStar/LETI-paper/internal/executor"
	"github.com/SosisterRapStar/LETI-paper/internal/inbox"
	"github.com/SosisterRapStar/LETI-paper/internal/observability/metrics"
	"github.com/SosisterRapStar/LETI-paper/internal/observability/tracing"
	"github.com/SosisterRapStar/LETI-paper/internal/outbox"
	"github.com/SosisterRapStar/LETI-paper/message"
	"github.com/SosisterRapStar/LETI-paper/retry"
	"github.com/SosisterRapStar/LETI-paper/step"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultPollInterval = 1 * time.Second
	defaultBatchSize    = 10
	defaultBackoffMin   = 100 * time.Millisecond
	defaultBackoffMax   = 1 * time.Minute
)

func NewMetrics(registry *prometheus.Registry, subsystem string) (*metrics.Metrics, error) {
	return metrics.New(registry, subsystem)
}

// Config содержит параметры для создания Controller.
// Subscriber и Publisher обязательны — без них невозможна работа с брокером.
// DB обязательна — без неё невозможна работа с outbox/inbox.
type Config struct {
	Subscriber broker.Subsciber
	Publisher  broker.Publisher
	DB         *database.DBContext

	// InfraRetry — политика retry для инфраструктурных ошибок (BeginTx, Commit, WriteOutbox).
	// Если nil — инфраструктурные операции выполняются без retry.
	InfraRetry *retry.Retrier

	// PollInterval — интервал поллинга outbox-таблицы (default: 1s).
	PollInterval time.Duration
	// BatchSize — размер батча при вычитке из outbox (default: 10).
	BatchSize int

	// BackoffPolicy — политика бэкоффа для ретрая публикации сообщений из outbox (default: Exponential).
	BackoffPolicy backoff.BackoffPolicy
	// BackoffMin — минимальный бэкофф (default: 100ms).
	BackoffMin time.Duration
	// BackoffMax — максимальный бэкофф (default: 1min).
	BackoffMax time.Duration

	// ErrCh — канал для ошибок Reader'а (неблокирующая отправка). Если nil — ошибки теряются.
	ErrCh chan<- error

	// Metrics — опциональные метрики саг (Prometheus). Если nil — метрики не собираются.
	// Создаётся через NewMetrics(registry, "saga").
	Metrics *metrics.Metrics

	// Tracing — опциональный трейсинг (спэны шагов, передача trace-контекста в сообщениях).
	// Если nil — спэны не создаются, inject/extract не вызываются.
	Tracing *TracingConfig
}

// TracingConfig задаёт параметры трейсинга. Передайте в Config.Tracing, чтобы включить трейсинг.
type TracingConfig struct {
	// Tracer — свой экземпляр trace.Tracer (например от своего TracerProvider с нужным sampler/экспортером).
	// Если задан — для спэнов используется он; иначе используется глобальный otel.Tracer(TracerName).
	// Получить: otel.Tracer("name") из глобального провайдера или myTracerProvider.Tracer("name").
	Tracer trace.Tracer

	// TracerName — имя для otel.Tracer(name), когда Tracer не задан; отображается в бэкенде как instrumentation scope.
	// Если пусто — используется "saga". Имеет смысл задать имя сервиса, например "order-service" или "myapp/saga".
	TracerName string
}

type Saga interface {
	Register(string, *step.Step) error
	StartSaga(context.Context, *step.Step, message.Message) error
}

// Controller управляет шагами саги.
// Отвечает за подписку на топики и маршрутизацию сообщений по типу (execute/failed).
// Делегирует выполнение шагов и управление транзакциями в StepExecutor.
//
// Init запускает фоновые процессы (reader) и выполняет миграции.
// Close останавливает reader.
type Controller struct {
	pubsub         broker.Subsciber
	executor       *executor.StepExecutor
	reader         *outbox.Reader
	dbCtx          *database.DBContext
	metrics        *metrics.Metrics
	tracingEnabled bool
	tracer         trace.Tracer
	tracerName     string
}

func New(cfg *Config) (*Controller, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if cfg.Subscriber == nil {
		return nil, fmt.Errorf("subscriber is required")
	}
	if cfg.Publisher == nil {
		return nil, fmt.Errorf("publisher is required")
	}
	if cfg.DB == nil {
		return nil, fmt.Errorf("db context is required")
	}

	pollInterval := cfg.PollInterval
	if pollInterval == 0 {
		pollInterval = defaultPollInterval
	}
	batchSize := cfg.BatchSize
	if batchSize == 0 {
		batchSize = defaultBatchSize
	}
	backoffPolicy := cfg.BackoffPolicy
	if backoffPolicy == nil {
		backoffPolicy = backoff.Expontential{}
	}
	backoffMin := cfg.BackoffMin
	if backoffMin == 0 {
		backoffMin = defaultBackoffMin
	}
	backoffMax := cfg.BackoffMax
	if backoffMax == 0 {
		backoffMax = defaultBackoffMax
	}

	errCh := cfg.ErrCh
	if errCh == nil {
		sink := make(chan error, 1)
		go func() {
			for range sink {
			}
		}()
		errCh = sink
	}

	w := outbox.NewWriter(cfg.DB)

	polling := outbox.NewPollingSettings(pollInterval, batchSize)
	boff := outbox.NewBackoffSettings(backoffPolicy, backoffMin, backoffMax)
	r := outbox.NewReader(cfg.DB, cfg.Publisher, polling, boff, errCh)

	inboxSvc := inbox.New(cfg.DB)

	tracingEnabled := cfg.Tracing != nil
	var tracer trace.Tracer
	tracerName := "saga"
	if cfg.Tracing != nil {
		tracer = cfg.Tracing.Tracer
		if cfg.Tracing.TracerName != "" {
			tracerName = cfg.Tracing.TracerName
		}
	}

	exec, err := executor.New(cfg.DB.DB(), w, inboxSvc, cfg.InfraRetry, cfg.Metrics, tracingEnabled, tracerName, tracer)
	if err != nil {
		return nil, fmt.Errorf("creating executor: %w", err)
	}

	return &Controller{
		pubsub:         cfg.Subscriber,
		executor:       exec,
		reader:         r,
		dbCtx:          cfg.DB,
		metrics:        cfg.Metrics,
		tracingEnabled: tracingEnabled,
		tracer:         tracer,
		tracerName:     tracerName,
	}, nil
}

// Init выполняет начальную инициализацию:
//  1. Применяет миграции outbox-таблицы (CREATE IF NOT EXISTS — идемпотентно).
//  2. Запускает фоновый процесс reader (polling outbox -> publish в брокер).
//
// Вызывать после Register, перед StartSaga.
func (c *Controller) Init(ctx context.Context) error {
	if err := c.runMigrations(ctx); err != nil {
		return fmt.Errorf("init: migrations: %w", err)
	}

	c.reader.Start(ctx)
	logger.Info("controller initialized: migrations applied, reader started")

	return nil
}

// Close останавливает reader и блокируется до завершения фоновых процессов (graceful shutdown).
func (c *Controller) Close() {
	c.reader.Close()
}

// runMigrations применяет миграции outbox и inbox в одной транзакции.
func (c *Controller) runMigrations(ctx context.Context) error {
	outboxSQL, err := c.migrationOutboxSQL()
	if err != nil {
		return err
	}
	inboxSQL, err := c.migrationInboxSQL()
	if err != nil {
		return err
	}

	tx, err := c.dbCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, outboxSQL); err != nil {
		return fmt.Errorf("exec outbox migration: %w", err)
	}
	if _, err := tx.ExecContext(ctx, inboxSQL); err != nil {
		return fmt.Errorf("exec inbox migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration tx: %w", err)
	}

	return nil
}

func (c *Controller) migrationOutboxSQL() (string, error) {
	switch c.dbCtx.Dialect() {
	case database.SQLDialectPostgres:
		return outbox.PostgresOutboxMigration, nil
	case database.SQLDialectMySQL:
		return outbox.MySQLOutboxMigration, nil
	default:
		return "", fmt.Errorf("unsupported dialect for migrations: %s", c.dbCtx.Dialect())
	}
}

func (c *Controller) migrationInboxSQL() (string, error) {
	switch c.dbCtx.Dialect() {
	case database.SQLDialectPostgres:
		return outbox.PostgresInboxMigration, nil
	case database.SQLDialectMySQL:
		return outbox.MySQLInboxMigration, nil
	default:
		return "", fmt.Errorf("unsupported dialect for inbox migration: %s", c.dbCtx.Dialect())
	}
}

// Register подписывается на топик и направляет входящие сообщения в соответствующий шаг.
func (c *Controller) Register(topic string, stp *step.Step) error {
	return c.pubsub.Subscribe(context.Background(), topic, func(ctx context.Context, msg message.Message) error {
		if c.tracingEnabled {
			ctx = tracing.ExtractTraceContext(ctx, &msg)
		}
		logger.Infof("received message from step=%s, saga=%s", msg.FromStep, msg.GetSagaID())

		msgType, err := msg.GetType()
		if err != nil {
			return err
		}

		var actionErr error
		switch msgType {
		case message.EventTypeComplete:
			_, actionErr = c.executor.ExecuteStep(ctx, stp, msg)
		case message.EventTypeFailed:
			actionErr = c.executor.CompensateStep(ctx, stp, msg)
		default:
			return fmt.Errorf("invalid message type: %q", msgType)
		}

		if actionErr != nil {
			if errors.Is(actionErr, inbox.ErrDuplicate) {
				return nil
			}
			logger.Warnf("action failed: %s", actionErr.Error())
		}
		return actionErr
	})
}

// StartSaga инициирует новую сагу: генерирует sagaID и запускает один шаг.
// Для последовательного запуска нескольких шагов используйте StartSagaWithSteps.
func (c *Controller) StartSaga(ctx context.Context, stp *step.Step, msg message.Message) error {
	_, err := c.StartSagaWithSteps(ctx, []*step.Step{stp}, msg)
	return err
}

// StartSagaWithSteps инициирует новую сагу и выполняет переданные шаги по очереди:
// результат каждого шага передаётся как вход следующему (в одном процессе, без брокера).
// Если какой-либо шаг вернёт ошибку, сага прерывается.
func (c *Controller) StartSagaWithSteps(ctx context.Context, steps []*step.Step, msg message.Message) (message.Message, error) {
	if len(steps) == 0 {
		return message.Message{}, fmt.Errorf("at least one step is required")
	}

	sagaID, err := generateSagaID()
	if err != nil {
		return message.Message{}, err
	}

	msg.SagaID = sagaID
	c.metrics.SagaStartedInc()

	if c.tracingEnabled {
		var sagaSpan trace.Span
		ctx, sagaSpan = tracing.StartSagaSpan(ctx, c.tracer, c.tracerName, sagaID)
		defer sagaSpan.End()
	}

	for _, stp := range steps {
		msg, err = c.executor.ExecuteStep(ctx, stp, msg)
		if err != nil {
			return message.Message{}, err
		}
	}
	return msg, nil
}

func generateSagaID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate saga id: %w", err)
	}
	return id.String(), nil
}
