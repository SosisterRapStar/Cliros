package inbox

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/database"
	"github.com/SosisterRapStar/LETI-paper/message"
)

const testSagaID = "01234567-89ab-cdef-0123-456789abcdef"

// TestInbox_Claim_Postgres_Success проверяет успешный claim в Postgres:
// INSERT возвращает строку (RETURNING) — сообщение считается первым, ошибки нет.
func TestInbox_Claim_Postgres_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := database.NewDBContext(db, database.SQLDialectPostgres)
	in := New(dbCtx)
	ctx := context.Background()
	sagaUUID := uuid.MustParse(testSagaID)
	msg := message.Message{MessageMeta: message.MessageMeta{SagaID: testSagaID, FromStep: "order-service"}}

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	mock.ExpectQuery("INSERT INTO\\s+public\\.inbox").
		WithArgs(sagaUUID, "order-service").
		WillReturnRows(sqlmock.NewRows([]string{"saga_id"}).AddRow(sagaUUID))

	err = in.Claim(ctx, tx, msg)
	if err != nil {
		t.Errorf("Claim: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestInbox_Claim_Postgres_Duplicate проверяет дедупликацию в Postgres:
// INSERT с ON CONFLICT не возвращает строк (RETURNING пустой) — возвращается ErrDuplicate.
func TestInbox_Claim_Postgres_Duplicate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := database.NewDBContext(db, database.SQLDialectPostgres)
	in := New(dbCtx)
	ctx := context.Background()
	msg := message.Message{MessageMeta: message.MessageMeta{SagaID: testSagaID, FromStep: "order-service"}}

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	mock.ExpectQuery("INSERT INTO\\s+public\\.inbox").
		WithArgs(uuid.MustParse(testSagaID), "order-service").
		WillReturnRows(sqlmock.NewRows([]string{"saga_id"}))

	err = in.Claim(ctx, tx, msg)
	if err == nil {
		t.Fatal("expected ErrDuplicate")
	}
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("got %v, want ErrDuplicate", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestInbox_Claim_InvalidSagaID проверяет, что при невалидном saga_id (не UUID)
// возвращается ошибка парсинга, а не ErrDuplicate.
func TestInbox_Claim_InvalidSagaID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := database.NewDBContext(db, database.SQLDialectPostgres)
	in := New(dbCtx)
	ctx := context.Background()
	msg := message.Message{MessageMeta: message.MessageMeta{SagaID: "not-a-uuid", FromStep: "order-service"}}

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	err = in.Claim(ctx, tx, msg)
	if err == nil {
		t.Fatal("expected invalid saga_id error")
	}
	if errors.Is(err, ErrDuplicate) {
		t.Errorf("expected parse error, not ErrDuplicate")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestInbox_Claim_Postgres_QueryError проверяет, что ошибка запроса к БД
// (например, обрыв соединения) возвращается как retryable, а не как ErrDuplicate.
func TestInbox_Claim_Postgres_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := database.NewDBContext(db, database.SQLDialectPostgres)
	in := New(dbCtx)
	ctx := context.Background()
	msg := message.Message{MessageMeta: message.MessageMeta{SagaID: testSagaID, FromStep: "order-service"}}

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	mock.ExpectQuery("INSERT INTO\\s+public\\.inbox").
		WithArgs(uuid.MustParse(testSagaID), "order-service").
		WillReturnError(sql.ErrConnDone)

	err = in.Claim(ctx, tx, msg)
	if err == nil {
		t.Fatal("expected query error")
	}
	if errors.Is(err, ErrDuplicate) {
		t.Errorf("expected conn error, not ErrDuplicate")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestInbox_Claim_MySQL_Success проверяет успешный claim в MySQL:
// Exec возвращает RowsAffected == 1 (вставлена новая строка) — ошибки нет.
func TestInbox_Claim_MySQL_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := database.NewDBContext(db, database.SQLDialectMySQL)
	in := New(dbCtx)
	ctx := context.Background()
	msg := message.Message{MessageMeta: message.MessageMeta{SagaID: testSagaID, FromStep: "order-service"}}

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	mock.ExpectExec("INSERT INTO inbox").
		WithArgs(testSagaID, "order-service").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = in.Claim(ctx, tx, msg)
	if err != nil {
		t.Errorf("Claim: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestInbox_Claim_MySQL_Duplicate проверяет дедупликацию в MySQL:
// ON DUPLICATE KEY UPDATE даёт RowsAffected == 2 — возвращается ErrDuplicate.
func TestInbox_Claim_MySQL_Duplicate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := database.NewDBContext(db, database.SQLDialectMySQL)
	in := New(dbCtx)
	ctx := context.Background()
	msg := message.Message{MessageMeta: message.MessageMeta{SagaID: testSagaID, FromStep: "order-service"}}

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	mock.ExpectExec("INSERT INTO inbox").
		WithArgs(testSagaID, "order-service").
		WillReturnResult(sqlmock.NewResult(0, 2))

	err = in.Claim(ctx, tx, msg)
	if err == nil {
		t.Fatal("expected ErrDuplicate")
	}
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("got %v, want ErrDuplicate", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestInbox_Claim_UnsupportedDialect проверяет, что для неподдерживаемого
// диалекта БД возвращается ошибка, а не ErrDuplicate.
func TestInbox_Claim_UnsupportedDialect(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := database.NewDBContext(db, database.SQLDialect("oracle"))
	in := New(dbCtx)
	ctx := context.Background()
	msg := message.Message{MessageMeta: message.MessageMeta{SagaID: testSagaID, FromStep: "step1"}}

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	err = in.Claim(ctx, tx, msg)
	if err == nil {
		t.Fatal("expected unsupported dialect error")
	}
	if errors.Is(err, ErrDuplicate) {
		t.Errorf("expected dialect error, not ErrDuplicate")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}
