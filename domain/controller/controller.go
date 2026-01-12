package controller

import (
	"context"
	"fmt"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/domain/step"
	"github.com/bytedance/gopkg/util/logger"
	"github.com/google/uuid"
)

// пользователь должен указать pubsub и еще должен указать, какие топики что делают
// то есть какие в какие топики пользователь должен отправить сообщение после обработки текущего
// пока что укажем описание топиков -, которые

// наверное это нужно делать в Step
// пусть описание топиков для отката и топиков для следующих действий будет не в контроллере, а step
type Controller struct {
	Pubsub broker.Pubsub
}

// как пользователь будет передавать переменные из своей функции в Message Payload
// то есть как пользователь будет передавать
// пусть в Step будет возврат сообщений

// короче у нас 1 топик, как для выполенения транзакций, так и для компенсации транзакций, получается, что мы
// определяем - что делать по типу сообщений
// вообще как будто мы должны регистровать не шаг, а execute или compensate, то есть сами функции
// то есть пусть step
// нам нужно сейчас как-то получить
func (c *Controller) Register(topic string, step step.Step) {
	var (
		ctx context.Context = context.Background()
	)
	// блять нужно что-то решить с контекстами иначе хуета тут будет полная нахуй
	c.Pubsub.Subscribe(ctx, topic, func(ctx context.Context, msg message.Message) error {
		var (
			sagaID   string = msg.GetSagaID()
			stepName string = msg.FromStep
		)

		logger.Info("got message from step: %s", stepName)
		logger.Info("during saga: %s", sagaID)

		msgType, err := msg.GetType()
		if err != nil {
			return err
		}

		switch msgType {
		case message.EventTypeComplete:
			if err := c.executeAction(ctx, step, msg); err != nil {
				logger.Warnf("error occured: %s", err.Error())
				return err
			}
		case message.EventTypeFailed:
			if err := c.compensateAction(ctx, step, msg); err != nil {
				logger.Warnf("error occured: %s", err.Error())
				return err
			}
		default:
			return fmt.Errorf("invalid message type: %q", msgType)
		}
		return nil
	})
}

func (c *Controller) generateSagaIDByUUIDV7() (string, error) {
	uuid, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return uuid.String(), nil
}

func (c *Controller) executeAction(ctx context.Context, stp step.Step, msg message.Message) error {
	// ctx := context.Background() и вот хуй же пойми чо тут с контекстом нахуй делать

	newMsg, err := stp.Execute(ctx, msg)
	if err != nil {
		return c.handleExecuteError(ctx, stp, msg, err)
	}

	return c.sendSuccessMessage(ctx, stp, newMsg)
}

func (c *Controller) handleExecuteError(ctx context.Context, stp step.Step, msg message.Message, err error) error {
	errHandler := stp.GetOnError()
	if errHandler == nil {
		return c.sendFailureMessage(ctx, stp, msg)
	}

	recoveryMsg, recoveryErr := errHandler(ctx, msg, err)
	if recoveryErr != nil {
		logger.Info("error handler failed, sending failure event")
		return c.sendFailureMessage(ctx, stp, recoveryMsg)
	}

	logger.Info("error handler succeeded, continuing saga")
	return c.sendSuccessMessage(ctx, stp, recoveryMsg)
}

func (c *Controller) sendSuccessMessage(ctx context.Context, stp step.Step, msg message.Message) error {
	msg.MessageType = message.EventTypeComplete
	if err := c.sendNextToExecute(ctx, stp, msg); err != nil {
		logger.Warnf("failed to send complete event to next services, sagaID: %s", msg.SagaID)
		return fmt.Errorf("saga execution failed: %w", err)
	}
	return nil
}

func (c *Controller) sendFailureMessage(ctx context.Context, stp step.Step, msg message.Message) error {
	msg.MessageType = message.EventTypeFailed
	if err := c.sendOnFail(ctx, stp, msg); err != nil {
		logger.Warnf("failed to send failure event, sagaID: %s", msg.SagaID)
		return fmt.Errorf("saga failure notification failed: %w", err)
	}
	logger.Info("failure event sent to previous services")
	return nil
}

func (c *Controller) sendOnFail(ctx context.Context, stp step.Step, msg message.Message) error {
	routing := stp.GetRouting()
	if len(routing.ErrorTopics) == 0 {
		logger.Info("no error topics configured, skipping failure notification")
		return nil
	}

	for _, topic := range routing.ErrorTopics {
		if err := c.Pubsub.Publish(ctx, topic, msg); err != nil {
			return fmt.Errorf("failed to publish to topic %s: %w", topic, err)
		}
	}
	logger.Info("failure events sent to all error topics")
	return nil
}

func (c *Controller) sendNextToExecute(ctx context.Context, stp step.Step, msg message.Message) error {
	routing := stp.GetRouting()
	if len(routing.NextStepTopics) == 0 {
		logger.Info("no next step topics configured")
		return nil
	}

	for _, topic := range routing.NextStepTopics {
		if err := c.Pubsub.Publish(ctx, topic, msg); err != nil {
			return fmt.Errorf("failed to publish to topic %s: %w", topic, err)
		}
	}
	logger.Info("complete messages sent to all next step topics")
	return nil
}

func (c *Controller) compensateAction(ctx context.Context, stp step.Step, msg message.Message) error {
	compensationMsg, err := stp.OnFail(ctx, msg)
	if err != nil {
		return c.handleCompensateError(ctx, stp, msg, err)
	}

	return c.sendCompensationSuccess(ctx, stp, compensationMsg)
}

func (c *Controller) handleCompensateError(ctx context.Context, stp step.Step, msg message.Message, err error) error {
	errHandler := stp.GetOnCompensateError()
	if errHandler == nil {
		logger.Warnf("compensation failed and no error handler configured, sagaID: %s", msg.SagaID)
		return fmt.Errorf("compensation failed: %w", err)
	}

	recoveryMsg, recoveryErr := errHandler(ctx, msg, err)
	if recoveryErr != nil {
		logger.Warnf("compensation error handler failed, sagaID: %s", msg.SagaID)
		return fmt.Errorf("compensation error handler failed: %w", recoveryErr)
	}

	logger.Info("compensation error handler succeeded, continuing compensation")
	return c.sendCompensationSuccess(ctx, stp, recoveryMsg)
}

func (c *Controller) sendCompensationSuccess(ctx context.Context, stp step.Step, msg message.Message) error {
	routing := stp.GetRouting()
	if len(routing.ErrorTopics) == 0 {
		logger.Info("no error topics configured for compensation propagation")
		return nil
	}

	msg.MessageType = message.EventTypeFailed
	for _, topic := range routing.ErrorTopics {
		if err := c.Pubsub.Publish(ctx, topic, msg); err != nil {
			return fmt.Errorf("failed to publish compensation to topic %s: %w", topic, err)
		}
	}
	logger.Info("compensation messages sent to all error topics")
	return nil
}

func (c *Controller) StartSaga(ctx context.Context, stp step.Step, msg message.Message) error {
	sagaID, err := c.generateSagaIDByUUIDV7()
	if err != nil {
		logger.Warn("error occured generating saga ID")
		return fmt.Errorf("failed to generate saga id: %w", err)
	}

	msg.SagaID = sagaID
	msg.FromStep = stp.Name()
	msg.MessageType = message.EventTypeComplete

	return c.executeAction(ctx, stp, msg)
}

// блин, нужно как-то придумать, как сделать что-то типа

// нужна какая-то функция типа execute

// сначала нужно разобраться, где будет логика посыла сообщения следующему сервису

// как будем публиковать, нам нужен pubsub еще и в самом step
// тогда нужно проки

// сейщас проблема в том, что у нас компенсация и регистрация идут в одном и том же шаге, то есть

// пользователь должен зарегестировать как

// пользователь по идее создает шаг и вешает шаги на топики
