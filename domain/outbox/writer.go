package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/SosisterRapStar/LETI-paper/domain/message"
)

var insertOutboxQuery = `
INSERT INTO saga.outbox (
	saga_id,
	step_name,
	topic,
	created_at,
	scheduled_at,
	metadata,
	payload
) VALUES ($1, $2, $3, $4, $5, $6, $7)
`

type Writer struct {
}

func NewWriter() *Writer {
	return &Writer{}
}

func (w *Writer) fromSagaToOutboxMessage(msg message.Message, topic, stepName string) (*OutboxMessage, error) {
	sagaUUID, err := uuid.Parse(msg.SagaID)
	if err != nil {
		return nil, fmt.Errorf("invalid saga_id format: %w", err)
	}

	payloadBytes, err := json.Marshal(msg.MessagePayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	metadataBytes, err := json.Marshal(msg.MessageMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	now := time.Now()
	return &OutboxMessage{
		SagaID:         sagaUUID,
		StepName:       stepName,
		Topic:          topic,
		CreatedAt:      now,
		ScheduledAt:    now,
		Metadata:       metadataBytes,
		Payload:        payloadBytes,
		AttemptCounter: 0,
		LastAttempt:    nil,
		ProcessedAt:    nil,
	}, nil
}

func (w *Writer) write(ctx context.Context, msg message.Message, tx pgx.Tx, topic, stepName string) error {
	outboxMsg, err := w.fromSagaToOutboxMessage(msg, topic, stepName)
	if err != nil {
		return fmt.Errorf("outbox convert message: %w", err)
	}

	_, err = tx.Exec(ctx, insertOutboxQuery,
		outboxMsg.SagaID,
		outboxMsg.StepName,
		outboxMsg.Topic,
		outboxMsg.CreatedAt,
		outboxMsg.ScheduledAt,
		outboxMsg.Metadata,
		outboxMsg.Payload,
	)
	if err != nil {
		return fmt.Errorf("outbox insert [saga_id=%s, step=%s, topic=%s]: %w", outboxMsg.SagaID, stepName, topic, err)
	}

	return nil
}

func (w *Writer) WriteMessages(ctx context.Context, msg message.Message, tx pgx.Tx, topics []string, stepName string) error {
	for _, topic := range topics {
		if err := w.write(ctx, msg, tx, topic, stepName); err != nil {
			return err
		}
	}
	return nil
}
