package outbox

import (
	"context"
	"time"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultInterval = 1 * time.Second

type PollingSettings struct {
	interval  time.Duration
	batchSize int
}

type Reader struct {
	Publisher       broker.Pubsub
	PollingSettings PollingSettings
	pool            *pgxpool.Pool
}

func NewPollingSettings(interval time.Duration, batchSize int) PollingSettings {
	return PollingSettings{
		interval:  interval,
		batchSize: batchSize,
	}
}

func (r *Reader) Start(ctx context.Context) {
	ticker := time.NewTicker(r.PollingSettings.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.scanTable(ctx)
		}
	}
}

func (r *Reader) Close() {

}

func (r *Reader) scanBatch(ctx context.Context) {
	conn, err := r.pool.Acquire(ctx)
	conn.
}
