package broker

import (
	"context"

	"github.com/SosisterRapStar/LETI-paper/domain/message"
)

// чтобы работать с библиотекой, пабсаб для обмена сообщениями должен реализовывать такой интерфейс
// нужен еще какокой-нибудь высококровневый интерфейс

type Publisher interface {
	Publish(ctx context.Context, topic string, message message.Message) error
}

type Subsciber interface {
	Subscribe(ctx context.Context, topic string, handler func(ctx context.Context, message message.Message) error) error
	Close() error
}

type Pubsub interface {
	Publisher
	Subsciber
}
