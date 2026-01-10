package controller

import (
	"context"
	"fmt"

	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/domain/step"
)

// чтобы работать с библиотекой, пабсаб для обмена сообщениями должен реализовывать такой интерфейс
// нужен еще какокой-нибудь высококровневый интерфейс
type Pubsub interface {
	Publish(ctx context.Context, topic string, message message.Message) error
	Subscribe(ctx context.Context, topic string, handler func(ctx context.Context, message message.Message) error) error
	Close() error
}

// пользователь должен указать pubsub и еще должен указать, какие топики что делают
// то есть какие в какие топики пользователь должен отправить сообщение после обработки текущего
// пока что укажем описание топиков -, которые

// наверное это нужно делать в Step
// пусть описание топиков для отката и топиков для следующих действий будет не в контроллере, а step
type Controller struct {
	Pubsub Pubsub
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
		var (
			msgType message.MessageType
		)
		msgType, err := msg.GetType()

		if err != nil {
			return err
		}

		switch msgType {
		// позже дополним эту логику до чего-нибудь,
		// по типу retry например
		case message.EventTypeExecute:
			step.Execute(ctx, msg)
		case message.EventTypeCompensate:
			step.Compensate(ctx, msg)
		default:
			return fmt.Errorf("invalid message type: %q", msgType)
		}
		return nil
	})
}

// сейщас проблема в том, что у нас компенсация и регистрация идут в одном и том же шаге, то есть

// пользователь должен зарегестировать как

// пользователь по идее создает шаг и вешает шаги на топики
