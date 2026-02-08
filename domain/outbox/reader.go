package outbox

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var batchQuery = `
SELECT 
	saga_id
	, step_name
	, topic
	, created_at
	, scheduled_at
	, metadata
	, payload
	, last_attempt
	, processed_at
FROM saga.outbox
	WHERE scheduled_at <= TIMESTAMP::NOW()
	ORDER BY created_at ASC 
	LIMIT $3
`

var updateOutboxQuery = `
UPDATE saga.outbox
SET 
    last_attempt = $3,
    scheduled_at = $4
WHERE 
    saga_id = $1 
    AND step_name = $2;
	`

type OutboxMessage struct {
	SagaID      uuid.UUID
	StepName    string
	Topic       string
	CreatedAt   time.Time
	ScheduledAt time.Time
	Metadata    []byte
	Payload     []byte
	ProcessedAt time.Time
}

const (
	defaultInterval  = 1 * time.Second
	defaultBatchSize = 10
)

type PollingSettings struct {
	// Интервал через который ходим в базку
	interval time.Duration
	// Размер батча, который вытаскиваем из базы
	batchSize int
}

type Reader struct {
	// Объект паблишера, через который публикуем сообщения
	Publisher broker.Pubsub
	// Настройкри поллера
	PollingSettings PollingSettings
	// Потом переделаем на собственную абстракцию
	pool *pgxpool.Pool

	// переиспользуемый буффер для паршеных сообщений
	// предпологается, что при рантайме приложения, на Reader будет выделяться только 1 горутина
	// поэтому не думаем о конкурентности в этом плане здесь
	// нужно не забыть, что после того когда создали такой буфер, его размеры нельзя менять никогда и не при каких условиях
	// пусть пока что будет так, что пользователь не сможет поменять буфер в рантайме, ему придется перезапускать приложение
	buffer []*OutboxMessage

	// Переменная, что стартовали
	started int32
	// Переменная, что закрылись
	closed int32

	// Банч оф булщит
	closeCtx  context.Context
	cancelCtx context.CancelFunc
	wg        sync.WaitGroup
	errCh     chan<- error
}

type ReaderParams struct {
	BatchSize *int
}

func (r *Reader) NewReader(rp ReaderParams) *Reader {
	// это пока не доделано
	var (
		buffer    []*OutboxMessage
		reader    Reader = Reader{}
		batchSize        = defaultBatchSize
	)
	if rp.BatchSize != nil {
		batchSize = *rp.BatchSize
	}
	buffer = make([]*OutboxMessage, batchSize)
	reader.buffer = buffer
	return &reader
}

func (r *Reader) IsStarted() int32 {
	// нужно подумать над mutex здесь
	return r.started
}

func NewPollingSettings(interval time.Duration, batchSize int) PollingSettings {
	return PollingSettings{
		interval:  interval,
		batchSize: batchSize,
	}
}

func (r *Reader) start(userCtx context.Context) {
	if !atomic.CompareAndSwapInt32(&r.started, 0, 1) {
		return
	}

	ticker := time.NewTicker(r.PollingSettings.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.closeCtx.Done():
			return
		case <-userCtx.Done():
			return
		case <-ticker.C:
			err := r.scanBatch(ctx)
			if err != nil {

			}
		}
	}
}

func (r *Reader) Close() {
	r.cancelCtx()

}

func (r *Reader) updateOutbox(ctx context.Context, attemptedAt time.Time, nextAttempt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	defer tx.Rollback(ctx)
	if err != nil {
		return err
	}
	tx.Exec(ctx)
}

func (r *Reader) publish(ctx context.Context, counter int) {
	for i := 0; i < counter; i++ {
		msg := r.buffer[i]
		topic := msg.Topic
		sagaMsg := r.fromOutboxToSagaMessage(*msg)
		if err := r.Publisher.Publish(ctx, topic, sagaMsg); err != nil {

		}
	}
}

func (r *Reader) fromOutboxToSagaMessage(oMsg OutboxMessage) message.Message {
	var (
		meta    message.MessageMeta
		payload message.MessagePayload
	)
	meta.FromStep = oMsg.StepName
	meta.SagaID = oMsg.StepName
	json.Unmarshal(oMsg.Payload, &payload)

	return message.Message{
		MessageMeta:    meta,
		MessagePayload: payload,
	}
}

func (r *Reader) scanBatch(ctx context.Context) (int, error) {
	var (
		rowsCounter = 0
	)
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := conn.Query(ctx, batchQuery)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	for rows.Next() {
		om := r.buffer[rowsCounter]
		rows.Scan(om.SagaID,
			om.StepName,
			om.Topic,
			om.CreatedAt,
			om.ScheduledAt,
			om.Metadata,
			om.Payload,
			om.ProcessedAt,
		)
		rowsCounter++
	}

	if err := rows.Err(); err != nil {
		return rowsCounter, err
	}

	return rowsCounter, nil
}
