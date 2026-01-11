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
	c.Pubsub.Subscribe(ctx, topic, func(ctx context.Context, msg message.Message) error {
		msgType, err := msg.GetType()
		if err != nil {
			return err
		}

		sagaID := msg.GetSagaID()
		if sagaID != "" {
			sagaID, err := c.generateSagaIDByUUIDV7()
			if err != nil {
				return fmt.Errorf("error occured generating saga id")
			}
		}

		switch msgType {
		// позже дополним эту логику до чего-нибудь,
		// по типу retry например
		case message.EventTypeComplete:

		case message.EventTypeFailed:
			step.OnFail(ctx, msg)
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

func (c *Controller) executeAction(stp step.Step, msg message.Message) error {
	var (
		ctx context.Context = context.Background()
	)

	newMsg, err := stp.Execute(ctx, msg) // тут нужно подумать, так как пользователь не заполняет sagaID и не должен иметь к нему доступа
	// sagaID должен быть обязательно закрытым параметром

	if err != nil {
		if errHandler := stp.GetOnError(); errHandler != nil {
			newMsg, err := errHandler(ctx, msg, err)
			if err != nil {
				err := c.sendOnFail(ctx, stp, newMsg)
				if err != nil {
					logger.Warn("error occured sending failed event to services")
					logger.Warnf("saga is down")
					return err
				}
				logger.Info("failed event sent to prev service")
				return nil
			} else {
				logger.Info("on error actions succesfully done")
				if err := c.sendNextToExecute(ctx, stp, newMsg); err != nil {
					logger.Warn("error occured sending complete events to next services")
					logger.Warnf("saga is down, sagaID: %s", msg.SagaID)
					return err
				}
			}
		} else {
			// тут хз какое отправлять сообщение
			// тут типа нужно отправить сообщение с правильным коннекстом, чтобы было понятно, что должен сделать другой сервис для отката
			c.sendOnFail(ctx, stp, msg)
		}
		// если произошла ошибка - то компенсируем, но вот нужно ли
		// а если на самом деле хочется сделать ретрай
	}
}

func (c *Controller) sendOnFail(ctx context.Context, stp step.Step, msg message.Message) error {
	var (
		routing step.RoutingConfig
	)
	routing = stp.GetRouting()
	if routing.ErrorTopics != nil {
		for _, topic := range routing.ErrorTopics {
			msg.Type = message.EventTypeFailed
			// тут нужен декоратор с ретраем для Publish обязательно
			// декоратор можно скрыть за интерфейсом и дать возможность пользователю выбирать
			// нужен ли ему ретрай или нет
			// хотя возможно ретрай и так есть в пабсаб библе для nats или kafka
			if err := c.Pubsub.Publish(ctx, topic, msg); err != nil {
				return err
			}
			logger.Info("fail events sent to all topics")
		}
		return nil
	}
	logger.Info("no any topics to send fail event")
	// если нет топиков для failed то ничего никуда не шлем
	return nil
}

func (c *Controller) sendNextToExecute(ctx context.Context, stp step.Step, msg message.Message) error {
	var (
		routing step.RoutingConfig
	)

	routing = stp.GetRouting()
	if routing.NextStepTopics != nil {
		for _, topic := range routing.NextStepTopics {
			msg.Type = message.EventTypeComplete
			// тут нужен декоратор с ретраем для Publish обязательно
			if err := c.Pubsub.Publish(ctx, topic, msg); err != nil {
				return err
			}
		}
		logger.Info("complete messages sent to all topics")
		return nil
	}
	logger.Info("no any next topics to send complete event")
	// если нет топиков для complete - ничего не отправляем
	return nil
}

func (c *Controller) compensateAction(step step.Step, msg message.Message) {

}

// блин, нужно как-то придумать, как сделать что-то типа

// нужна какая-то функция типа execute

// сначала нужно разобраться, где будет логика посыла сообщения следующему сервису

// как будем публиковать, нам нужен pubsub еще и в самом step
// тогда нужно проки

// сейщас проблема в том, что у нас компенсация и регистрация идут в одном и том же шаге, то есть

// пользователь должен зарегестировать как

// пользователь по идее создает шаг и вешает шаги на топики
