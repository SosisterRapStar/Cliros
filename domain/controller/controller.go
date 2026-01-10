package controller

import (
	"context"

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

type Controller struct {
	Pubsub Pubsub
}

// как пользователь будет передавать переменные из своей функции в Message Payload
// пусть в Step будет возврат сообщений
func (c *Controller) RegisterStep(topic string, step step.Step) {

}
