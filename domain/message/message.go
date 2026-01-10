package message

type Message struct {
	Type     string
	SagaID   string
	ActionID string
	Payload  map[string]any
}
