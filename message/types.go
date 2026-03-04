package message

type MessageType string

var (
	EventTypeComplete MessageType = "execute"
	EventTypeFailed   MessageType = "failed"
)
