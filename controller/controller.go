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
	"github.com/SosisterRapStar/LETI-paper/internal/outbox"
	"github.com/SosisterRapStar/LETI-paper/message"
	"github.com/SosisterRapStar/LETI-paper/retry"
	"github.com/SosisterRapStar/LETI-paper/step"
)

const (
	defaultPollInterval = 1 * time.Second
	defaultBatchSize    = 10
	defaultBackoffMin   = 100 * time.Millisecond
	defaultBackoffMax   = 1 * time.Minute
)

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
	pubsub   broker.Subsciber
	executor *executor.StepExecutor
	reader   *outbox.Reader
	dbCtx    *database.DBContext
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

	exec, err := executor.New(cfg.DB.DB(), w, inboxSvc, cfg.InfraRetry)
	if err != nil {
		return nil, fmt.Errorf("creating executor: %w", err)
	}

	return &Controller{
		pubsub:   cfg.Subscriber,
		executor: exec,
		reader:   r,
		dbCtx:    cfg.DB,
	}, nil
}

// Init выполняет начальную инициализацию:
//  1. Применяет миграции outbox-таблицы (CREATE IF NOT EXISTS — идемпотентно).
//  2. Запускает фоновый процесс reader (polling outbox → publish в брокер).
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
		logger.Infof("received message from step=%s, saga=%s", msg.FromStep, msg.GetSagaID())

		msgType, err := msg.GetType()
		if err != nil {
			return err
		}

		var actionErr error
		switch msgType {
		case message.EventTypeComplete:
			actionErr = c.executor.ExecuteStep(ctx, stp, msg)
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

// StartSaga инициирует новую сагу: генерирует sagaID и запускает первый шаг.
func (c *Controller) StartSaga(ctx context.Context, stp *step.Step, msg message.Message) error {
	sagaID, err := generateSagaID()
	if err != nil {
		return err
	}

	msg.SagaID = sagaID
	return c.executor.ExecuteStep(ctx, stp, msg)
}

func generateSagaID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate saga id: %w", err)
	}
	return id.String(), nil
}
