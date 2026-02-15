package adaptors

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SosisterRapStar/LETI-paper/domain/outbox/migrations"
)

type Postgres struct {
	pool *pgxpool.Pool
}

func (p *Postgres) Close() {
	p.pool.Close()
}

func NewPostgres(ctx context.Context, durl string) *Postgres {
	pool, err := pgxpool.New(ctx, durl)
	if err != nil {
		log.Fatal(err)
	}
	return &Postgres{
		pool: pool,
	}
}

func (p *Postgres) RunMigration(ctx context.Context) error {
	tx, err := p.pool.Begin(ctx)
	defer tx.Rollback(ctx) //nolint:errcheck
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, migrations.PostgresOutboxMigration)
	if err != nil {
		return err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return err
	}
	return nil
}
