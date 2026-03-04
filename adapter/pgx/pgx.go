package pgx

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
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
