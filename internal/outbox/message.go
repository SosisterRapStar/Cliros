package outbox

import (
	"time"

	"github.com/google/uuid"
)

// SagaType отличает фазу саги, в которой было записано сообщение в outbox.
// Нужен как часть PK (saga_id, topic, saga_type), чтобы один и тот же шаг
// мог писать в один и тот же topic и при execute, и при compensate, не ловя
// коллизию первичного ключа.
//
// TODO (временное решение): корректнее уйти на идемпотентный event_id.
type SagaType string

const (
	SagaTypeExecute    SagaType = "execute"
	SagaTypeCompensate SagaType = "compensate"
)

type OutboxMessage struct {
	SagaID         uuid.UUID
	StepName       string
	Topic          string
	SagaType       SagaType
	CreatedAt      time.Time
	ScheduledAt    time.Time
	Metadata       []byte
	Payload        []byte
	AttemptCounter uint
	LastAttempt    *time.Time
	ProcessedAt    *time.Time
}
