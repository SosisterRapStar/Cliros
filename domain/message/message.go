package message

import (
	"fmt"
	"slices"
)

type Message struct {
	Type    MessageType    `json:"type"`
	SagaID  string         `json:"saga_id"`
	StepID  string         `json:"step_id"`
	Payload map[string]any `json:"payload"`
}

var validTypes = []MessageType{
	EventTypeExecute,
	EventTypeCompensate,
	EventTypeRetry,
	EventTypeCompleted,
	EventTypeFailed,
}

func (m *Message) GetType() (MessageType, error) {
	if !slices.Contains(validTypes, m.Type) {
		return "", fmt.Errorf("invalid message type: %q", m.Type)
	}
	return m.Type, nil
}
