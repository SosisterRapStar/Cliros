package databases

import (
	"context"
	"database/sql"
	"fmt"
)

// SQLDialect представляет собой диалект для sql
type SQLDialect string

// Поддерживаемые диалекты
const (
	SQLDialectPostgres SQLDialect = "postgres"
	SQLDialectMySQL    SQLDialect = "mysql"
)

// Queryer интерфейс над кварями
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// TxQueryer интерфейс над тем что может кварить транзакция
type TxQueryer interface {
	Queryer
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Tx интерфейс над транзакцией бд
type Tx interface {
	Commit() error
	Rollback() error
	TxQueryer
}

// DB интерфейс над соединением базы данных
type DB interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error)
	Queryer
}

// DBContext хранит данные о диалекте бд
type DBContext struct {
	db      DB
	dialect SQLDialect
}

// NewDBContext создает контекст с имплементацией через sql.DB
func NewDBContext(db *sql.DB, dialect SQLDialect) *DBContext {
	return NewDBContextWithDB(&dbAdapter{db: db}, dialect)
}

// NewDBContextWithDB создает новый контекст базы данных с отдельной имплементацией базы данных
func NewDBContextWithDB(db DB, dialect SQLDialect) *DBContext {
	c := &DBContext{
		db:      db,
		dialect: dialect,
	}

	return c
}

func (c *DBContext) DB() DB {
	return c.db
}

func (c *DBContext) Dialect() SQLDialect {
	return c.dialect
}

// GetSQLPlaceholder возвращает плейсхолдеры для разных диалектов баз данных
func (c *DBContext) GetSQLPlaceholder(index int) string {
	switch c.dialect {
	case SQLDialectPostgres:
		return fmt.Sprintf("$%d", index)
	default:
		return "?"
	}
}

// txAdapter адаптор для интерфейса транзакции через sql.Tx
type txAdapter struct {
	tx *sql.Tx
}

func (a *txAdapter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return a.tx.ExecContext(ctx, query, args...)
}

func (a *txAdapter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return a.tx.QueryContext(ctx, query, args...)
}

func (a *txAdapter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return a.tx.QueryRowContext(ctx, query, args...)
}

func (a *txAdapter) Commit() error {
	return a.tx.Commit()
}

func (a *txAdapter) Rollback() error {
	return a.tx.Rollback()
}

// dbAdapter адаптор для интерефеса соединения с базой через sql.DB
type dbAdapter struct {
	db *sql.DB
}

func (a *dbAdapter) BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error) {
	tx, err := a.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &txAdapter{tx: tx}, nil
}

func (a *dbAdapter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return a.db.ExecContext(ctx, query, args...)
}

func (a *dbAdapter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return a.db.QueryContext(ctx, query, args...)
}
