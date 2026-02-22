package writer

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/SosisterRapStar/LETI-paper/domain/databases"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
)

const testSagaID = "01234567-89ab-cdef-0123-456789abcdef"

func testMessage() message.Message {
	return message.Message{
		MessageMeta:    message.MessageMeta{SagaID: testSagaID, FromStep: "order-service"},
		MessagePayload: message.MessagePayload{Payload: map[string]any{"order_id": "123"}},
	}
}

// TestWriter_WriteMessages_Success_OneTopic проверяет успешную запись одного сообщения в outbox
// для одного топика внутри переданной транзакции.
func TestWriter_WriteMessages_Success_OneTopic(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	mock.ExpectExec("INSERT INTO saga.outbox").
		WithArgs(
			uuid.MustParse(testSagaID),
			"order-service",
			"payment.process",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = w.WriteMessages(ctx, testMessage(), tx, []string{"payment.process"}, "order-service")
	if err != nil {
		t.Errorf("WriteMessages: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteMessages_Success_TwoTopics проверяет запись одного сообщения в два топика —
// вызывается два INSERT (по одному на топик).
func TestWriter_WriteMessages_Success_TwoTopics(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	mock.ExpectExec("INSERT INTO saga.outbox").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "topic1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO saga.outbox").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "topic2", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = w.WriteMessages(ctx, testMessage(), tx, []string{"topic1", "topic2"}, "order-service")
	if err != nil {
		t.Errorf("WriteMessages: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteMessages_InvalidSagaID проверяет, что при невалидном saga_id
// возвращается ошибка до обращения к БД.
func TestWriter_WriteMessages_InvalidSagaID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	msg := testMessage()
	msg.SagaID = "not-a-uuid"

	err = w.WriteMessages(ctx, msg, tx, []string{"topic1"}, "step1")
	if err == nil {
		t.Fatal("expected invalid saga_id error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteMessages_ExecError проверяет, что ошибка INSERT пробрасывается
// из WriteMessages.
func TestWriter_WriteMessages_ExecError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	mock.ExpectExec("INSERT INTO saga.outbox").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err = w.WriteMessages(ctx, testMessage(), tx, []string{"topic1"}, "step1")
	if err == nil {
		t.Fatal("expected exec error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteTx_Success проверяет полный цикл WriteTx: BeginTx, WriteMessages (INSERT),
// Commit без пользовательской функции.
func TestWriter_WriteTx_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO saga.outbox").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = w.WriteTx(ctx, testMessage(), []string{"topic1"}, "step1", nil)
	if err != nil {
		t.Errorf("WriteTx: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteTx_Success_WithTxWorkFunc проверяет WriteTx с пользовательской функцией fn:
// сначала выполняется fn, затем запись в outbox и Commit.
func TestWriter_WriteTx_Success_WithTxWorkFunc(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	var fnCalled bool
	fn := func(_ context.Context, _ databases.TxQueryer) error {
		fnCalled = true
		return nil
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO saga.outbox").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = w.WriteTx(ctx, testMessage(), []string{"topic1"}, "step1", fn)
	if err != nil {
		t.Errorf("WriteTx: %v", err)
	}
	if !fnCalled {
		t.Error("expected fn to be called")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteTx_BeginError проверяет, что ошибка BeginTx возвращается из WriteTx.
func TestWriter_WriteTx_BeginError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	mock.ExpectBegin().WillReturnError(sql.ErrConnDone)

	err = w.WriteTx(ctx, testMessage(), []string{"topic1"}, "step1", nil)
	if err == nil {
		t.Fatal("expected begin tx error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteTx_CommitError проверяет, что ошибка Commit возвращается из WriteTx.
func TestWriter_WriteTx_CommitError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO saga.outbox").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(sql.ErrTxDone)

	err = w.WriteTx(ctx, testMessage(), []string{"topic1"}, "step1", nil)
	if err == nil {
		t.Fatal("expected commit error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteTx_FnError проверяет, что при ошибке пользовательской функции
// транзакция откатывается и WriteMessages не вызывается.
func TestWriter_WriteTx_FnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	fnErr := sql.ErrNoRows
	fn := func(_ context.Context, _ databases.TxQueryer) error {
		return fnErr
	}

	mock.ExpectBegin()
	mock.ExpectRollback()

	err = w.WriteTx(ctx, testMessage(), []string{"topic1"}, "step1", fn)
	if err == nil {
		t.Fatal("expected fn error")
	}
	if err != fnErr {
		t.Errorf("got %v, want %v", err, fnErr)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}

// TestWriter_WriteMessages_EmptyTopics проверяет, что при пустом списке топиков
// WriteMessages не выполняет INSERT и возвращает nil.
func TestWriter_WriteMessages_EmptyTopics(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	dbCtx := databases.NewDBContext(db, databases.SQLDialectPostgres)
	w := New(dbCtx)
	ctx := context.Background()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	err = w.WriteMessages(ctx, testMessage(), tx, nil, "step1")
	if err != nil {
		t.Errorf("WriteMessages: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("expectations: %v", err)
	}
}
