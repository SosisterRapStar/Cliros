package message

import (
	"fmt"
	"slices"
)

type Message struct {
	MessageType MessageType `json:"type"`
	FromStep    string      `json:"from"`
	SagaID      string      `json:"saga_id"`
	// StepName string         `json:"step_name"`
	Payload map[string]any `json:"payload"`
}

var validTypes = []MessageType{
	EventTypeComplete,
	EventTypeFailed,
}

func (m *Message) GetType() (MessageType, error) {
	if !slices.Contains(validTypes, m.MessageType) {
		return "", fmt.Errorf("invalid message type: %q", m.MessageType)
	}
	return m.MessageType, nil
}

func (m *Message) GetSagaID() string {
	return m.SagaID
}

func (m *Message) SetSagaID() {

}
