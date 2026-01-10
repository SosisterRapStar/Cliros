package controller

import "context"

type Message struct {
	SagaID   string
	ActionID string
	Type     string
	Payload  map[string]any
}

type Pubsub interface {
	Publish(ctx context.Context, topic string, message Message) error
	Subscribe(ctx context.Context, topic string, handler func(ctx context.Context, message Message) error) error
	Close() error
}

type Controller struct {
	Pubsub Pubsub
}
