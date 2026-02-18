package controller

import (
	"context"
	"fmt"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/databases"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/domain/outbox/writer"
	"github.com/SosisterRapStar/LETI-paper/domain/step"
)

type Saga interface {
	Register(string, *step.Step) error
	StartSaga(context.Context, *step.Step, message.Message) error
}

// Controller управляет шагами саги.
// Использует pubsub для подписки на входящие сообщения,
// writer + dbCtx обеспечивают атомарную запись бизнес-логики и outbox-сообщений в одной транзакции.
type Controller struct {
	pubsub broker.Subsciber
	writer *writer.Writer
	dbCtx  *databases.DBContext
}

func New(pubsub broker.Subsciber, w *writer.Writer, dbCtx *databases.DBContext) (*Controller, error) {
	if pubsub == nil {
		return nil, fmt.Errorf("pubsub is required")
	}
	if w == nil {
		return nil, fmt.Errorf("writer is required")
	}
	if dbCtx == nil {
		return nil, fmt.Errorf("db context is required")
	}
	return &Controller{
		pubsub: pubsub,
		writer: w,
		dbCtx:  dbCtx,
	}, nil
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
			actionErr = c.executeAction(ctx, stp, msg)
		case message.EventTypeFailed:
			actionErr = c.compensateAction(ctx, stp, msg)
		default:
			return fmt.Errorf("invalid message type: %q", msgType)
		}

		if actionErr != nil {
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
	msg.FromStep = stp.Name()
	msg.MessageType = message.EventTypeComplete

	return c.executeAction(ctx, stp, msg)
}

// executeAction выполняет шаг саги внутри managed-транзакции:
// 1. Begin TX
// 2. stp.Execute(ctx, tx, msg) — пользовательская бизнес-логика
// 3. Writer.WriteMessages — запись в outbox в той же TX
// 4. Commit
func (c *Controller) executeAction(ctx context.Context, stp *step.Step, msg message.Message) error {
	tx, err := c.dbCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	newMsg, err := stp.Execute(ctx, tx, msg)
	if err != nil {
		// Явный rollback до вызова error handler'а — освобождаем блокировки,
		// чтобы error handler мог начать свою транзакцию без дедлока.
		_ = tx.Rollback() //nolint:errcheck
		return c.handleExecuteError(ctx, stp, msg, err)
	}

	newMsg.SagaID = msg.SagaID

	routing := stp.GetRouting()
	if err := c.writeOutboxInTx(ctx, tx, stp, newMsg, message.EventTypeComplete, routing.NextStepTopics); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	logger.Info("execute action committed, messages written to outbox")
	return nil
}

// compensateAction выполняет компенсацию внутри managed-транзакции.
func (c *Controller) compensateAction(ctx context.Context, stp *step.Step, msg message.Message) error {
	tx, err := c.dbCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	compensationMsg, err := stp.OnFail(ctx, tx, msg)
	if err != nil {
		_ = tx.Rollback() //nolint:errcheck
		return c.handleCompensateError(ctx, stp, msg, err)
	}

	compensationMsg.SagaID = msg.SagaID

	routing := stp.GetRouting()
	if err := c.writeOutboxInTx(ctx, tx, stp, compensationMsg, message.EventTypeFailed, routing.ErrorTopics); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit compensation tx: %w", err)
	}

	logger.Info("compensation committed, messages written to outbox")
	return nil
}

// handleExecuteError обрабатывает ошибку выполнения шага.
// Если есть ErrorHandler — вызывает его в отдельной транзакции.
func (c *Controller) handleExecuteError(ctx context.Context, stp *step.Step, msg message.Message, execErr error) error {
	errHandler := stp.GetOnError()
	if errHandler == nil {
		return c.publishEvent(ctx, stp, msg, message.EventTypeFailed, stp.GetRouting().ErrorTopics)
	}

	tx, err := c.dbCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin error handler tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	recoveryMsg, recoveryErr := errHandler(ctx, tx, msg, execErr)
	if recoveryErr != nil {
		logger.Info("error handler failed, sending failure event")
		return c.publishEvent(ctx, stp, recoveryMsg, message.EventTypeFailed, stp.GetRouting().ErrorTopics)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit error handler tx: %w", err)
	}

	logger.Info("error handler succeeded, continuing saga")
	return c.publishEvent(ctx, stp, recoveryMsg, message.EventTypeComplete, stp.GetRouting().NextStepTopics)
}

// handleCompensateError обрабатывает ошибку компенсации.
func (c *Controller) handleCompensateError(ctx context.Context, stp *step.Step, msg message.Message, compErr error) error {
	errHandler := stp.GetOnCompensateError()
	if errHandler == nil {
		logger.Warnf("compensation failed and no error handler configured, sagaID: %s", msg.SagaID)
		return fmt.Errorf("compensation failed: %w", compErr)
	}

	tx, err := c.dbCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin compensate error handler tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	recoveryMsg, recoveryErr := errHandler(ctx, tx, msg, compErr)
	if recoveryErr != nil {
		logger.Warnf("compensation error handler failed, sagaID: %s", msg.SagaID)
		return fmt.Errorf("compensation error handler failed: %w", recoveryErr)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit compensate error handler tx: %w", err)
	}

	logger.Info("compensation error handler succeeded, continuing compensation")
	return c.publishEvent(ctx, stp, recoveryMsg, message.EventTypeFailed, stp.GetRouting().ErrorTopics)
}

// writeOutboxInTx записывает сообщение в outbox в рамках существующей транзакции.
func (c *Controller) writeOutboxInTx(
	ctx context.Context,
	tx databases.TxQueryer,
	stp *step.Step,
	msg message.Message,
	msgType message.MessageType,
	topics []string,
) error {
	if len(topics) == 0 {
		return nil
	}

	msg.MessageType = msgType
	msg.FromStep = stp.Name()

	if err := c.writer.WriteMessages(ctx, msg, tx, topics, stp.Name()); err != nil {
		return fmt.Errorf("outbox write: %w", err)
	}
	return nil
}

// publishEvent записывает событие в outbox через отдельную транзакцию.
func (c *Controller) publishEvent(
	ctx context.Context,
	stp *step.Step,
	msg message.Message,
	msgType message.MessageType,
	topics []string,
) error {
	if len(topics) == 0 {
		logger.Infof("no topics configured for event type %s", msgType)
		return nil
	}

	msg.MessageType = msgType
	msg.FromStep = stp.Name()

	if err := c.writer.WriteTx(ctx, msg, topics, stp.Name(), nil); err != nil {
		return fmt.Errorf("publish event [type=%s]: %w", msgType, err)
	}
	return nil
}

func generateSagaID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate saga id: %w", err)
	}
	return id.String(), nil
}
