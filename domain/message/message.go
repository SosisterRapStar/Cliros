package message

type Message struct {
	SagaID   string
	ActionID string
	Type     string
	Payload  map[string]any
}
