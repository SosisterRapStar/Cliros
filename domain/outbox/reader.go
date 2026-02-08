package outbox

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

const defaultInterval = 1 * time.Second

type PollingSettings struct {
	interval  time.Duration
	batchSize int
}

type Reader struct {
	Publisher       broker.Pubsub
	PollingSettings PollingSettings
	pool            *pgxpool.Pool

	started int32
	closed  int32

	closeCtx context.Context
	cancel   context.CancelFunc
	errCh    chan<- error
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

func (r *Reader) Start(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&r.started, 0, 1) {
		return
	}

	ticker := time.NewTicker(r.PollingSettings.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			err := r.scanTable(ctx)
			if err != nil {

			}
		}
	}
}

func (r *Reader) Close() {

}

func (r *Reader) scanBatch(ctx context.Context) error {
	tx, err := p.db.Begin
}
