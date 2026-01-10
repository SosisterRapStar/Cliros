package message

type MessageType string

var (
	EventTypeExecute    MessageType = "execute"
	EventTypeCompensate MessageType = "compensate"
	EventTypeRetry      MessageType = "retry"
	EventTypeCompleted  MessageType = "completed"
	EventTypeFailed     MessageType = "failed"
)
