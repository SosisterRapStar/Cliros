package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"

	"github.com/SosisterRapStar/LETI-paper/domain/message"
)

type Client struct {
	conn   *nats.Conn
	subs   map[string]*nats.Subscription
	mu     sync.RWMutex
	closed bool
}

type Config struct {
	URL string
}

func New(config Config, options ...nats.Option) (*Client, error) {
	if config.URL == "" {
		config.URL = nats.DefaultURL
	}

	conn, err := nats.Connect(config.URL, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	return &Client{
		conn: conn,
		subs: make(map[string]*nats.Subscription),
	}, nil
}

func (c *Client) Publish(ctx context.Context, topic string, message message.Message) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return fmt.Errorf("client is closed")
	}

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if err := c.conn.Publish(topic, data); err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

func (c *Client) Subscribe(ctx context.Context, topic string, handler func(ctx context.Context, message message.Message) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return fmt.Errorf("client is closed")
	}

	if _, exists := c.subs[topic]; exists {
		return fmt.Errorf("already subscribed to topic: %s", topic)
	}

	sub, err := c.conn.Subscribe(topic, func(msg *nats.Msg) {
		var message message.Message
		if err := json.Unmarshal(msg.Data, &message); err != nil {
			return
		}

		if err := handler(context.Background(), message); err != nil { // вызывается пользовательский обработчик
			return
		}
	})
	if err != nil {
		return fmt.Errorf("failed to subscribe to topic %s: %w", topic, err)
	}

	c.subs[topic] = sub
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	for topic, sub := range c.subs {
		if err := sub.Unsubscribe(); err != nil {
			return fmt.Errorf("failed to unsubscribe from topic %s: %w", topic, err)
		}
		delete(c.subs, topic)
	}

	c.conn.Close()
	c.closed = true
	return nil
}

func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && c.conn.IsConnected()
}
