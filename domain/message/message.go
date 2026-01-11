package message

import (
	"fmt"
	"slices"
)

type Message struct {
	typ    MessageType `json:"type"`
	sagaID string      `json:"saga_id"`
	// StepName string         `json:"step_name"`
	Payload map[string]any `json:"payload"`
}

var validTypes = []MessageType{
	EventTypeComplete,
	EventTypeFailed,
}

func (m *Message) GetType() (MessageType, error) {
	if !slices.Contains(validTypes, m.typ) {
		return "", fmt.Errorf("invalid message type: %q", m.typ)
	}
	return m.typ, nil
}

func (m *Message) GetSagaID() string {
	return m.sagaID
}
