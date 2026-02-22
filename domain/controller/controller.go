package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/databases"
	"github.com/SosisterRapStar/LETI-paper/domain/executor"
	"github.com/SosisterRapStar/LETI-paper/domain/inbox"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/domain/outbox/migrations"
	"github.com/SosisterRapStar/LETI-paper/domain/outbox/reader"
	"github.com/SosisterRapStar/LETI-paper/domain/step"
)

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
	reader   *reader.Reader
	dbCtx    *databases.DBContext
}

func New(
	pubsub broker.Subsciber,
	exec *executor.StepExecutor,
	r *reader.Reader,
	dbCtx *databases.DBContext,
) (*Controller, error) {
	if pubsub == nil {
		return nil, fmt.Errorf("pubsub is required")
	}
	if exec == nil {
		return nil, fmt.Errorf("executor is required")
	}
	if r == nil {
		return nil, fmt.Errorf("reader is required")
	}
	if dbCtx == nil {
		return nil, fmt.Errorf("db context is required")
	}
	return &Controller{
		pubsub:   pubsub,
		executor: exec,
		reader:   r,
		dbCtx:    dbCtx,
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
	case databases.SQLDialectPostgres:
		return migrations.PostgresOutboxMigration, nil
	case databases.SQLDialectMySQL:
		return migrations.MySQLOutboxMigration, nil
	default:
		return "", fmt.Errorf("unsupported dialect for migrations: %s", c.dbCtx.Dialect())
	}
}

func (c *Controller) migrationInboxSQL() (string, error) {
	switch c.dbCtx.Dialect() {
	case databases.SQLDialectPostgres:
		return migrations.PostgresInboxMigration, nil
	case databases.SQLDialectMySQL:
		return migrations.MySQLInboxMigration, nil
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
