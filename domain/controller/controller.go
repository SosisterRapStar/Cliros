package controller

import (
	"context"
	"fmt"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/executor"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/domain/step"
)

type Saga interface {
	Register(string, *step.Step) error
	StartSaga(context.Context, *step.Step, message.Message) error
}

// Controller управляет шагами саги.
// Отвечает за подписку на топики и маршрутизацию сообщений по типу (execute/failed).
// Делегирует выполнение шагов и управление транзакциями в StepExecutor.
type Controller struct {
	pubsub   broker.Subsciber
	executor *executor.StepExecutor
}

func New(pubsub broker.Subsciber, exec *executor.StepExecutor) (*Controller, error) {
	if pubsub == nil {
		return nil, fmt.Errorf("pubsub is required")
	}
	if exec == nil {
		return nil, fmt.Errorf("executor is required")
	}
	return &Controller{
		pubsub:   pubsub,
		executor: exec,
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
			actionErr = c.executor.ExecuteStep(ctx, stp, msg)
		case message.EventTypeFailed:
			actionErr = c.executor.CompensateStep(ctx, stp, msg)
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
	return c.executor.ExecuteStep(ctx, stp, msg)
}

func generateSagaID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate saga id: %w", err)
	}
	return id.String(), nil
}
