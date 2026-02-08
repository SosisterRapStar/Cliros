package message

import (
	"fmt"
	"slices"
)

type (
	MessageMeta struct {
		MessageType MessageType `json:"type"`
		FromStep    string      `json:"from"`
		SagaID      string      `json:"saga_id"`
	}

	MessagePayload struct {
		Payload map[string]any `json:"payload"`
	}

	Message struct {
		MessageMeta
		MessagePayload
	}
)

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
