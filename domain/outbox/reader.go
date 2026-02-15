package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/databases"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/thirdparty/backoff"
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
}

type Reader struct {
	// Объект паблишера, через который публикуем сообщения
	Publisher broker.Publisher
	// Настройки и опции
	PollingSettings PollingSettings
	BackoffSettings BackoffSettings
	// Абстракция над базой данных
	dbCtx *databases.DBContext

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

func NewReader(dbCtx *databases.DBContext, publisher broker.Publisher, polling PollingSettings, boff BackoffSettings, errCh chan<- error) *Reader {
	batchSize := polling.batchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	ctx, cancel := context.WithCancel(context.Background())

	buffer := make([]*OutboxMessage, batchSize)
	for i := range buffer {
		buffer[i] = &OutboxMessage{}
	}

	return &Reader{
		Publisher:       publisher,
		PollingSettings: polling,
		BackoffSettings: boff,
		dbCtx:           dbCtx,
		buffer:          buffer,
		closeCtx:        ctx,
		cancelCtx:       cancel,
		errCh:           errCh,
	}
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
func (r *Reader) sendError(err error) {
	select {
	case r.errCh <- err:
	default:
	}
}

func (r *Reader) processMessage(ctx context.Context, msg *OutboxMessage) error {
	sagaMsg, err := fromOutboxToSagaMessage(msg)
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

// buildBatchQuery строит SELECT-запрос для вычитки сообщений из outbox.
func (r *Reader) buildBatchQuery() string {
	p := r.dbCtx.GetSQLPlaceholder
	return fmt.Sprintf(`
SELECT 
	saga_id, step_name, topic, created_at, scheduled_at,
	metadata, payload, attempts_counter, last_attempt, processed_at
FROM %s
WHERE scheduled_at <= NOW() AND processed_at IS NULL
ORDER BY created_at ASC 
LIMIT %s`, "saga.outbox", p(1))
}

// buildUpdateOnErrQuery строит UPDATE-запрос при ошибке публикации.
// args: $1=saga_id, $2=step_name, $3=last_attempt, $4=scheduled_at
func (r *Reader) buildUpdateOnErrQuery() string {
	p := r.dbCtx.GetSQLPlaceholder
	return fmt.Sprintf(`
UPDATE %s
SET 
	attempts_counter = attempts_counter + 1,
	last_attempt = %s,
	scheduled_at = %s
WHERE 
	saga_id = %s 
	AND step_name = %s`,
		"saga.outbox", p(3), p(4), p(1), p(2)) //nolint:mnd
}

// buildUpdateOnSuccessQuery строит UPDATE-запрос при успешной публикации.
// args: $1=saga_id, $2=step_name, $3=processed_at
func (r *Reader) buildUpdateOnSuccessQuery() string {
	p := r.dbCtx.GetSQLPlaceholder
	return fmt.Sprintf(`
UPDATE %s
SET 
	processed_at = %s
WHERE 
	saga_id = %s 
	AND step_name = %s`,
		"saga.outbox", p(3), p(1), p(2)) //nolint:mnd
}

func (r *Reader) updateOutboxOnSuccess(
	ctx context.Context,
	msg *OutboxMessage,
	processedAt time.Time,
) error {
	tx, err := r.dbCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	query := r.buildUpdateOnSuccessQuery()
	_, err = tx.ExecContext(ctx, query, msg.SagaID, msg.StepName, processedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Reader) updateOutboxOnErr(
	ctx context.Context,
	msg *OutboxMessage,
	lastAttempt, nextAttempt time.Time,
) error {
	tx, err := r.dbCtx.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	query := r.buildUpdateOnErrQuery()
	_, err = tx.ExecContext(ctx, query, msg.SagaID, msg.StepName, lastAttempt, nextAttempt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func fromOutboxToSagaMessage(oMsg *OutboxMessage) (message.Message, error) {
	var (
		meta    message.MessageMeta
		payload message.MessagePayload
	)
	meta.FromStep = oMsg.StepName
	meta.SagaID = oMsg.SagaID.String()
	if err := json.Unmarshal(oMsg.Payload, &payload); err != nil {
		return message.Message{}, err
	}

	return message.Message{
		MessageMeta:    meta,
		MessagePayload: payload,
	}, nil
}

func (r *Reader) scanBatch(ctx context.Context) (int, error) {
	rowsCounter := 0

	query := r.buildBatchQuery()
	rows, err := r.dbCtx.DB().QueryContext(ctx, query, r.PollingSettings.batchSize)
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
