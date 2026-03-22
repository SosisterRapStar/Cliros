package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SosisterRapStar/cliros/backoff"
	"github.com/SosisterRapStar/cliros/broker"
	"github.com/SosisterRapStar/cliros/database"
	"github.com/SosisterRapStar/cliros/message"
	"github.com/bytedance/gopkg/util/logger"
)

const (
	defaultInterval  = 1 * time.Second
	defaultBatchSize = 10
)

// BackoffSettings настройки бэкоффа, решает, на какое время запланировать ретрай сообщения
type BackoffSettings struct {
	backoffPolicy backoff.BackoffPolicy
	backoffMin    time.Duration
	backoffMax    time.Duration
}

// PollingSettings настройки поллера, решает как часто будут политься сообщения и сколько сообщений будет вычитано
type PollingSettings struct {
	interval  time.Duration
	batchSize int
}

type Reader struct {
	Publisher       broker.Publisher
	PollingSettings PollingSettings
	BackoffSettings BackoffSettings
	dbCtx           *database.DBContext

	buffer []*OutboxMessage

	started int32
	closed  int32

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

func NewReader(dbCtx *database.DBContext, publisher broker.Publisher, polling PollingSettings, boff BackoffSettings, errCh chan<- error) *Reader {
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

// Close останавливает reader и блокируется до завершения горутины поллинга (graceful shutdown).
// Текущий обрабатываемый батч будет допроцессен перед выходом.
// Безопасен для повторного вызова.
func (r *Reader) Close() {
	if !atomic.CompareAndSwapInt32(&r.closed, 0, 1) {
		return
	}
	r.cancelCtx()
	r.wg.Wait()
}

func (r *Reader) polling(userCtx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.closeCtx.Done():
			return

		case <-userCtx.Done():
			return

		case <-ticker.C:
			logger.Info("polling outbox")
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
LIMIT %s`, "public.outbox", p(1))
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
		"public.outbox", p(3), p(4), p(1), p(2)) //nolint:mnd
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
		"public.outbox", p(3), p(1), p(2)) //nolint:mnd
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
	if len(oMsg.Metadata) > 0 {
		if err := json.Unmarshal(oMsg.Metadata, &meta); err != nil {
			return message.Message{}, fmt.Errorf("unmarshal outbox metadata: %w", err)
		}
	}
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
	logger.Info("scanning batch")
	rowsCounter := 0

	query := r.buildBatchQuery()
	logger.Infof("scanning batch query: %s", query)
	rows, err := r.dbCtx.DB().QueryContext(ctx, query, 10)
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
