package outbox

import (
	"time"

	"github.com/google/uuid"
)

type OutboxMessage struct {
	SagaID         uuid.UUID
	StepName       string
	Topic          string
	CreatedAt      time.Time
	ScheduledAt    time.Time
	Metadata       []byte
	Payload        []byte
	AttemptCounter uint
	LastAttempt    *time.Time
	ProcessedAt    *time.Time
}
