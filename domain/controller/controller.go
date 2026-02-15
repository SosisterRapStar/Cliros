package controller

import (
	"context"
	"fmt"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/databases"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/domain/outbox"
	"github.com/SosisterRapStar/LETI-paper/domain/step"
)

type Saga interface {
	Register(string, *step.Step) error
	StartSaga(context.Context, *step.Step, message.Message) error
}

// Controller управляет шагами саги.
// Pubsub используется для подписки на входящие сообщения.
// Writer + DBCtx обеспечивают атомарную запись бизнес-логики и outbox-сообщений в одной транзакции.
// Reader вычитывает outbox и публикует сообщения в брокер.
type Controller struct {
	Pubsub broker.Subsciber
	Writer *outbox.Writer
	DBCtx  *databases.DBContext
}

// Register подписывается на топик и направляет входящие сообщения в соответствующий шаг.
func (c *Controller) Register(topic string, stp *step.Step) error {
	ctx := context.Background()
	return c.Pubsub.Subscribe(ctx, topic, func(ctx context.Context, msg message.Message) error {
		sagaID := msg.GetSagaID()
		stepName := msg.FromStep

		logger.Info("got message from step: %s", stepName)
		logger.Info("during saga: %s", sagaID)

		msgType, err := msg.GetType()
		if err != nil {
			return err
		}

		switch msgType {
		case message.EventTypeComplete:
			if err := c.executeAction(ctx, stp, msg); err != nil {
				logger.Warnf("error occurred: %s", err.Error())
				return err
			}
		case message.EventTypeFailed:
			if err := c.compensateAction(ctx, stp, msg); err != nil {
				logger.Warnf("error occurred: %s", err.Error())
				return err
			}
		default:
			return fmt.Errorf("invalid message type: %q", msgType)
		}
		return nil
	})
}

func (c *Controller) generateSagaIDByUUIDV7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// executeAction выполняет шаг саги внутри managed-транзакции:
// 1. Begin TX
// 2. stp.Execute(ctx, tx, msg) -- пользовательская бизнес-логика
// 3. Writer.WriteMessages -- запись в outbox в той же TX
// 4. Commit
func (c *Controller) executeAction(ctx context.Context, stp *step.Step, msg message.Message) error {
	tx, err := c.DBCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback() //nolint:errcheck
		}
	}()

	newMsg, err := stp.Execute(ctx, tx, msg)
	if err != nil {
		// Execute failed -- rollback business tx, handle error separately
		_ = tx.Rollback() //nolint:errcheck
		committed = true  // prevent double rollback in defer
		return c.handleExecuteError(ctx, stp, msg, err)
	}

	// Write success outbox messages in the same tx
	newMsg.MessageType = message.EventTypeComplete
	newMsg.SagaID = msg.SagaID
	newMsg.FromStep = stp.Name()

	routing := stp.GetRouting()
	if len(routing.NextStepTopics) > 0 {
		if err := c.Writer.WriteMessages(ctx, newMsg, tx, routing.NextStepTopics, stp.Name()); err != nil {
			return fmt.Errorf("outbox write next steps: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true

	logger.Info("execute action committed, messages written to outbox")
	return nil
}

// handleExecuteError обрабатывает ошибку выполнения шага.
// Если есть ErrorHandler, вызывает его. Результат записывается в outbox через отдельную TX.
func (c *Controller) handleExecuteError(ctx context.Context, stp *step.Step, msg message.Message, execErr error) error {
	errHandler := stp.GetOnError()
	if errHandler == nil {
		return c.writeFailureToOutbox(ctx, stp, msg)
	}

	recoveryMsg, recoveryErr := errHandler(ctx, msg, execErr)
	if recoveryErr != nil {
		logger.Info("error handler failed, sending failure event")
		return c.writeFailureToOutbox(ctx, stp, recoveryMsg)
	}

	logger.Info("error handler succeeded, continuing saga")
	return c.writeSuccessToOutbox(ctx, stp, recoveryMsg)
}

// compensateAction выполняет компенсацию внутри managed-транзакции.
func (c *Controller) compensateAction(ctx context.Context, stp *step.Step, msg message.Message) error {
	tx, err := c.DBCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	var committed bool
	defer func() {
		if !committed {
			_ = tx.Rollback() //nolint:errcheck
		}
	}()

	compensationMsg, err := stp.OnFail(ctx, tx, msg)
	if err != nil {
		_ = tx.Rollback() //nolint:errcheck
		committed = true
		return c.handleCompensateError(ctx, stp, msg, err)
	}

	// Propagate failure to previous steps via outbox
	compensationMsg.MessageType = message.EventTypeFailed
	compensationMsg.SagaID = msg.SagaID
	compensationMsg.FromStep = stp.Name()

	routing := stp.GetRouting()
	if len(routing.ErrorTopics) > 0 {
		if err := c.Writer.WriteMessages(ctx, compensationMsg, tx, routing.ErrorTopics, stp.Name()); err != nil {
			return fmt.Errorf("outbox write compensation: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit compensation tx: %w", err)
	}
	committed = true

	logger.Info("compensation committed, messages written to outbox")
	return nil
}

// handleCompensateError обрабатывает ошибку компенсации.
func (c *Controller) handleCompensateError(ctx context.Context, stp *step.Step, msg message.Message, compErr error) error {
	errHandler := stp.GetOnCompensateError()
	if errHandler == nil {
		logger.Warnf("compensation failed and no error handler configured, sagaID: %s", msg.SagaID)
		return fmt.Errorf("compensation failed: %w", compErr)
	}

	recoveryMsg, recoveryErr := errHandler(ctx, msg, compErr)
	if recoveryErr != nil {
		logger.Warnf("compensation error handler failed, sagaID: %s", msg.SagaID)
		return fmt.Errorf("compensation error handler failed: %w", recoveryErr)
	}

	logger.Info("compensation error handler succeeded, continuing compensation")
	return c.writeCompensationToOutbox(ctx, stp, recoveryMsg)
}

// writeSuccessToOutbox записывает success-сообщение в outbox через отдельную транзакцию.
func (c *Controller) writeSuccessToOutbox(ctx context.Context, stp *step.Step, msg message.Message) error {
	msg.MessageType = message.EventTypeComplete
	msg.FromStep = stp.Name()

	routing := stp.GetRouting()
	if len(routing.NextStepTopics) == 0 {
		logger.Info("no next step topics configured")
		return nil
	}

	return c.Writer.WriteTx(ctx, msg, routing.NextStepTopics, stp.Name(), nil)
}

// writeFailureToOutbox записывает failure-сообщение в outbox через отдельную транзакцию.
func (c *Controller) writeFailureToOutbox(ctx context.Context, stp *step.Step, msg message.Message) error {
	msg.MessageType = message.EventTypeFailed
	msg.FromStep = stp.Name()

	routing := stp.GetRouting()
	if len(routing.ErrorTopics) == 0 {
		logger.Info("no error topics configured, skipping failure notification")
		return nil
	}

	if err := c.Writer.WriteTx(ctx, msg, routing.ErrorTopics, stp.Name(), nil); err != nil {
		logger.Warnf("failed to write failure to outbox, sagaID: %s", msg.SagaID)
		return fmt.Errorf("saga failure notification failed: %w", err)
	}
	logger.Info("failure event written to outbox")
	return nil
}

// writeCompensationToOutbox записывает compensation-сообщение в outbox.
func (c *Controller) writeCompensationToOutbox(ctx context.Context, stp *step.Step, msg message.Message) error {
	msg.MessageType = message.EventTypeFailed
	msg.FromStep = stp.Name()

	routing := stp.GetRouting()
	if len(routing.ErrorTopics) == 0 {
		logger.Info("no error topics configured for compensation propagation")
		return nil
	}

	if err := c.Writer.WriteTx(ctx, msg, routing.ErrorTopics, stp.Name(), nil); err != nil {
		return fmt.Errorf("failed to write compensation to outbox: %w", err)
	}
	logger.Info("compensation messages written to outbox")
	return nil
}

// StartSaga инициирует новую сагу: генерирует sagaID и запускает первый шаг.
func (c *Controller) StartSaga(ctx context.Context, stp *step.Step, msg message.Message) error {
	sagaID, err := c.generateSagaIDByUUIDV7()
	if err != nil {
		logger.Warn("error occurred generating saga ID")
		return fmt.Errorf("failed to generate saga id: %w", err)
	}

	msg.SagaID = sagaID
	msg.FromStep = stp.Name()
	msg.MessageType = message.EventTypeComplete

	return c.executeAction(ctx, stp, msg)
}
