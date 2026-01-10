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
func (c *Controller) RegisterStep(topic string, step step.Step) {
	// должны зарегестировать с хэндлер на выполнение действия при получении сообщения како-го либо типа
	// пользователь хочет получать сообщения и данные из них, поэтому наверное нужно

	//
}
