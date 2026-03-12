package inbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/database"
	"github.com/SosisterRapStar/LETI-paper/message"
	"github.com/SosisterRapStar/LETI-paper/retry"
)

// ErrDuplicate возвращается, когда входящее saga-сообщение уже было обработано (дубликат по ключу идемпотентности).
// Контроллер при этой ошибке должен вернуть nil (ACK без повторного выполнения).
var ErrDuplicate = errors.New("saga message already processed")

// Inbox — дедупликация входящих сообщений по ключу (saga_id, from_step).
type Inbox struct {
	dbCtx *database.DBContext
}

// New создаёт Inbox с подстановкой плейсхолдеров под диалект БД.
func New(dbCtx *database.DBContext) *Inbox {
	return &Inbox{dbCtx: dbCtx}
}

// Claim вставляет запись в inbox по (saga_id, from_step). При CONFLICT возвращает ErrDuplicate.
// Вызывается в начале транзакции; tx не коммитится здесь.
func (in *Inbox) Claim(ctx context.Context, tx database.TxQueryer, msg message.Message) error {
	sagaUUID, err := uuid.Parse(msg.SagaID)
	if err != nil {
		return fmt.Errorf("inbox claim: invalid saga_id: %w", err)
	}

	switch in.dbCtx.Dialect() {
	case database.SQLDialectPostgres:
		return in.claimPostgres(ctx, tx, sagaUUID, msg.FromStep)
	case database.SQLDialectMySQL:
		return in.claimMySQL(ctx, tx, sagaUUID, msg.FromStep)
	default:
		return fmt.Errorf("inbox: unsupported dialect %s", in.dbCtx.Dialect())
	}
}

func (in *Inbox) buildInsertClaimQueryPostgres() string {
	p := in.dbCtx.GetSQLPlaceholder
	return fmt.Sprintf(`
INSERT INTO inbox (saga_id, from_step) VALUES (%s, %s)
ON CONFLICT (saga_id, from_step) DO NOTHING
RETURNING saga_id`, p(1), p(2)) //nolint: mnd
}

func (in *Inbox) claimPostgres(ctx context.Context, tx database.TxQueryer, sagaUUID uuid.UUID, fromStep string) error {
	query := in.buildInsertClaimQueryPostgres()
	var out uuid.UUID
	err := tx.QueryRowContext(ctx, query, sagaUUID, fromStep).Scan(&out)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDuplicate
		}
		return retry.AsRetryable(fmt.Errorf("inbox claim: %w", err))
	}
	return nil
}

func (in *Inbox) buildInsertClaimQueryMySQL() string {
	p := in.dbCtx.GetSQLPlaceholder
	return fmt.Sprintf(`
INSERT INTO inbox (saga_id, from_step) VALUES (%s, %s)
ON DUPLICATE KEY UPDATE from_step = VALUES(from_step)`, p(1), p(2)) //nolint: mnd
}

func (in *Inbox) claimMySQL(ctx context.Context, tx database.TxQueryer, sagaUUID uuid.UUID, fromStep string) error {
	query := in.buildInsertClaimQueryMySQL()
	result, err := tx.ExecContext(ctx, query, sagaUUID.String(), fromStep)
	if err != nil {
		return retry.AsRetryable(fmt.Errorf("inbox claim: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return retry.AsRetryable(fmt.Errorf("inbox claim rows affected: %w", err))
	}
	if affected == 2 { //nolint: mnd
		return ErrDuplicate
	}
	return nil
}
