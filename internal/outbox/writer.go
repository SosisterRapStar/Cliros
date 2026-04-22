package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/SosisterRapStar/cliros/database"
	"github.com/SosisterRapStar/cliros/message"
)

// TxWorkFunc пользователь может указать здесь функцию, которая должна быть выполнена транзакционно
// writer вызовет ее в одной транзакции с отправкой сообщения в outbox таблицу
type TxWorkFunc func(ctx context.Context, tx database.TxQueryer) error

// Writer отвечает за запись сообщений внутри пользовательской транзакции в outbox таблицу
type Writer struct {
	dbCtx *database.DBContext
}

func NewWriter(dbCtx *database.DBContext) *Writer {
	return &Writer{dbCtx: dbCtx}
}

// buildInsertQuery строит инсерт с оутбокс сообщением
func (w *Writer) buildInsertQuery() string {
	p := w.dbCtx.GetSQLPlaceholder
	return fmt.Sprintf(`
INSERT INTO %s (
	saga_id, step_name, topic, saga_type,
	created_at, scheduled_at, metadata, payload
) VALUES (%s, %s, %s, %s, %s, %s, %s, %s)`,
		qualifiedOutboxTable(w.dbCtx.Dialect()),
		p(1), p(2), p(3), p(4), p(5), p(6), p(7), p(8)) //nolint:mnd
}

func (w *Writer) fromSagaToOutboxMessage(msg message.Message, topic, stepName string, sagaType SagaType) (*OutboxMessage, error) {
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

	now := time.Now().UTC()
	return &OutboxMessage{
		SagaID:         sagaUUID,
		StepName:       stepName,
		Topic:          topic,
		SagaType:       sagaType,
		CreatedAt:      now,
		ScheduledAt:    now,
		Metadata:       metadataBytes,
		Payload:        payloadBytes,
		AttemptCounter: 0,
		LastAttempt:    nil,
		ProcessedAt:    nil,
	}, nil
}

// write сохраняет сообщение в outbox таблицу
func (w *Writer) write(ctx context.Context, msg message.Message, tx database.TxQueryer, topic, stepName string, sagaType SagaType) error {
	outboxMsg, err := w.fromSagaToOutboxMessage(msg, topic, stepName, sagaType)
	if err != nil {
		return fmt.Errorf("outbox convert message: %w", err)
	}

	query := w.buildInsertQuery()
	_, err = tx.ExecContext(ctx, query,
		outboxMsg.SagaID,
		outboxMsg.StepName,
		outboxMsg.Topic,
		string(outboxMsg.SagaType),
		outboxMsg.CreatedAt,
		outboxMsg.ScheduledAt,
		outboxMsg.Metadata,
		outboxMsg.Payload,
	)
	if err != nil {
		return fmt.Errorf("outbox insert [saga_id=%s, step=%s, topic=%s, saga_type=%s]: %w",
			outboxMsg.SagaID, stepName, topic, sagaType, err)
	}

	return nil
}

// WriteMessages запишет внутри транзации сообщения в базку для каждого топика.
// sagaType отличает execute- и compensate-записи одного шага (входит в PK outbox).
func (w *Writer) WriteMessages(
	ctx context.Context,
	msg message.Message,
	tx database.TxQueryer,
	topics []string,
	stepName string,
	sagaType SagaType,
) error {
	for _, topic := range topics {
		if err := w.write(ctx, msg, tx, topic, stepName, sagaType); err != nil {
			return err
		}
	}
	return nil
}

// WriteTx начинает транзакцию, вызывает бизнес функцию пользователя, которая должна произойти перед
// отправкой сообщения в outbox.
// sagaType отличает execute- и compensate-записи одного шага (входит в PK outbox).
func (w *Writer) WriteTx(
	ctx context.Context,
	msg message.Message,
	topics []string,
	stepName string,
	sagaType SagaType,
	fn TxWorkFunc,
) error {
	tx, err := w.dbCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("outbox begin tx: %w", err)
	}

	defer tx.Rollback() //nolint:errcheck

	if fn != nil {
		if err := fn(ctx, tx); err != nil {
			return err
		}
	}

	if err := w.WriteMessages(ctx, msg, tx, topics, stepName, sagaType); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("outbox commit tx: %w", err)
	}

	return nil
}
