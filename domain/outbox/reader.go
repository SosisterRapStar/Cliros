package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/thirdparty/backoff"
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
	, attempts_counter
	, last_attempt
	, processed_at
FROM saga.outbox
	WHERE scheduled_at <= NOW() AND processed_at IS NULL
	ORDER BY created_at ASC 
	LIMIT $1
`

var updateOutboxQueryOnErr = `
UPDATE saga.outbox
SET 
	attempt_counter = attempt_counter + 1,
    last_attempt = $3,
    scheduled_at = $4
WHERE 
    saga_id = $1 
    AND step_name = $2;
	`
var updateOutboxQueryOnSuccess = `
	UPDATE saga.outbox
	SET 
		processed_at = $3
	WHERE 
		saga_id = $1 
		AND step_name = $2;
		`

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

const (
	defaultInterval  = 1 * time.Second
	defaultBatchSize = 10
)

// Настройки бэкоффа, решает, на какое время запланировать ретрай сообщения
type BackoffSettings struct {
	backoffPolicy backoff.BackoffPolicy
	backoffMin    time.Duration
	backoffMax    time.Duration
}

// Настройки поллера, решает как часто будут политься сообщения и сколько сообщений будет вычитано
type PollingSettings struct {
	// Интервал через который ходим в базку
	interval time.Duration
	// Размер батча, который вытаскиваем из базы
	batchSize int
	// Политика с которой будет выбираться следующее время попытки отдачи сообщения
}

type Reader struct {
	// Объект паблишера, через который публикуем сообщения
	Publisher broker.Publisher
	// Настройки и опции
	PollingSettings PollingSettings
	BackoffSettings BackoffSettings
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

func NewBackoffSettings(p backoff.BackoffPolicy, minB, maxB time.Duration) BackoffSettings {
	return BackoffSettings{
		backoffPolicy: p,
		backoffMin:    minB,
		backoffMax:    maxB,
	}
}

func NewPollingSettings(interval time.Duration, batchSize int) PollingSettings {
	return PollingSettings{
		interval:  interval,
		batchSize: batchSize,
	}
}

func (r *Reader) NewReader(rp ReaderParams) *Reader {
	// это пока не доделано
	var (
		buffer    []*OutboxMessage
		reader    = Reader{}
		batchSize = defaultBatchSize
	)
	if rp.BatchSize != nil {
		batchSize = *rp.BatchSize
	}
	buffer = make([]*OutboxMessage, batchSize)
	reader.buffer = buffer
	return &reader
}

func (r *Reader) IsStarted() bool {
	return atomic.LoadInt32(&r.started) == 1
}

func (r *Reader) IsClosed() bool {
	return atomic.LoadInt32(&r.closed) == 1
}

func (r *Reader) Start(userCtx context.Context) {
	if atomic.LoadInt32(&r.closed) == 1 {
		return
	}
	if !atomic.CompareAndSwapInt32(&r.started, 0, 1) {
		return
	}
	r.wg.Go(
		func() {
			r.polling(userCtx)
		},
	)
}

func (r *Reader) Close() {
	if !atomic.CompareAndSwapInt32(&r.closed, 0, 1) {
		return
	}
	r.cancelCtx()
}

func (r *Reader) polling(userCtx context.Context) {
	ticker := time.NewTicker(r.PollingSettings.interval)
	defer ticker.Stop()
	for {
		select {
		// если закрыли ридер
		case <-r.closeCtx.Done():
			return

		// если юзер ctx отработал
		case <-userCtx.Done():
			return

		// TODO: такая логика тут как-будто не подходит
		// TODO: в будущем заменить такую логику на другую
		// мы должны опираться на то с какой скоростью отработает функция вычитки
		// если что-то пойдет не так и будет отправлять или читать сообщения долго
		// то тикер захочет сложить 2 тика в канал и таким образом после обработки сообщений
		// сразу подхватим следующий тик - а мы бы хотели подождать время следующего полла, без учитывания походов в бд и кафку
		case <-ticker.C:
			readed, err := r.scanBatch(userCtx)
			if err != nil {
				r.sendError(fmt.Errorf("outbox scan batch: %w", err))
			}
			for i := 0; i < readed; i++ {
				if err := r.processMessage(userCtx, r.buffer[i]); err != nil {
					r.sendError(fmt.Errorf("outbox process batch: %w", err))
				}
			}
		}
	}
}

// sendError неблокирующая отправка ошибки в канал ошибок.
// Если канал заполнен — ошибка дропается, чтобы ридер не зависал.
func (r *Reader) sendError(err error) {
	select {
	case r.errCh <- err:
	default:
	}
}

func (r *Reader) processMessage(ctx context.Context, msg *OutboxMessage) error {
	sagaMsg, err := r.fromOutboxToSagaMessage(msg)
	if err != nil {
		return err
	}
	// TODO: нужно добавить свои собственные ошибки, чтобы как-то различать,
	// какие ошибки пришли от паблишера и связаны ли они вообще с сообщениями
	// обязательно их логировать
	if err := r.Publisher.Publish(ctx, msg.Topic, sagaMsg); err != nil {
		r.sendError(fmt.Errorf("outbox publish [saga_id=%s, step=%s]: %w", msg.SagaID, msg.StepName, err))
		r.handlePublishError(ctx, msg)
		return nil
	}
	r.handlePublishSuccess(ctx, msg)
	return nil
}

func (r *Reader) handlePublishError(ctx context.Context, msg *OutboxMessage) {
	attemptTime := time.Now()
	nextAttempt := r.calculateNextAttempt(msg)
	if err := r.updateOutboxOnErr(ctx, msg, attemptTime, nextAttempt); err != nil {
		r.sendError(fmt.Errorf("outbox update on error [saga_id=%s, step=%s]: %w", msg.SagaID, msg.StepName, err))
	}
}

func (r *Reader) handlePublishSuccess(ctx context.Context, msg *OutboxMessage) {
	if err := r.updateOutboxOnSuccess(ctx, msg, time.Now()); err != nil {
		r.sendError(fmt.Errorf("outbox update on success [saga_id=%s, step=%s]: %w", msg.SagaID, msg.StepName, err))
	}
}

func (r *Reader) calculateNextAttempt(msg *OutboxMessage) time.Time {
	backoffDuration := r.BackoffSettings.backoffPolicy.CalcBackoff(
		msg.AttemptCounter,
		r.BackoffSettings.backoffMin,
		r.BackoffSettings.backoffMax,
	)
	return time.Now().Add(backoffDuration)
}

func (r *Reader) updateOutboxOnSuccess(
	ctx context.Context,
	msg *OutboxMessage,
	processedAt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	defer tx.Rollback(ctx) //nolint:errcheck
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, updateOutboxQueryOnSuccess, msg.SagaID, msg.StepName, processedAt)
	if err != nil {
		return err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (r *Reader) updateOutboxOnErr(
	ctx context.Context,
	msg *OutboxMessage,
	lastAttempt time.Time,
	nextAttempt time.Time) error {
	tx, err := r.pool.Begin(ctx)
	defer tx.Rollback(ctx) //nolint:errcheck
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, updateOutboxQueryOnErr, msg.SagaID, msg.StepName, lastAttempt, nextAttempt)
	if err != nil {
		return err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (r *Reader) fromOutboxToSagaMessage(oMsg *OutboxMessage) (message.Message, error) {
	var (
		meta    message.MessageMeta
		payload message.MessagePayload
	)
	meta.FromStep = oMsg.StepName
	meta.SagaID = oMsg.StepName
	if err := json.Unmarshal(oMsg.Payload, &payload); err != nil {
		return message.Message{}, err
	}

	return message.Message{
		MessageMeta:    meta,
		MessagePayload: payload,
	}, nil
}

func (r *Reader) scanBatch(ctx context.Context) (int, error) {
	var (
		rowsCounter = 0
	)
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := conn.Query(ctx, batchQuery, r.PollingSettings.batchSize)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	for rows.Next() {
		om := r.buffer[rowsCounter]
		if err := rows.Scan(
			&om.SagaID,
			&om.StepName,
			&om.Topic,
			&om.CreatedAt,
			&om.ScheduledAt,
			&om.Metadata,
			&om.Payload,
			&om.AttemptCounter,
			&om.LastAttempt,
			&om.ProcessedAt,
		); err != nil {
			return rowsCounter, fmt.Errorf("outbox scan row: %w", err)
		}
		rowsCounter++
	}

	if err := rows.Err(); err != nil {
		return rowsCounter, err
	}

	return rowsCounter, nil
}
