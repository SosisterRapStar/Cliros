package executor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SosisterRapStar/LETI-paper/domain/databases"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/domain/outbox/writer"
	"github.com/SosisterRapStar/LETI-paper/domain/step"
	"github.com/SosisterRapStar/LETI-paper/thirdparty/retrier"
)

// --- Mocks ---

const testSagaID = "01234567-89ab-cdef-0123-456789abcdef"

type zeroBackoff struct{}

func (z zeroBackoff) CalcBackoff(_ uint, _, _ time.Duration) time.Duration { return 0 }

type mockResult struct{}

func (m mockResult) LastInsertId() (int64, error) { return 0, nil }
func (m mockResult) RowsAffected() (int64, error) { return 1, nil }

type mockTx struct {
	commitErr error
}

func (m *mockTx) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return mockResult{}, nil
}

func (m *mockTx) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}

func (m *mockTx) QueryRowContext(_ context.Context, _ string, _ ...any) *sql.Row {
	return nil
}

func (m *mockTx) Commit() error   { return m.commitErr }
func (m *mockTx) Rollback() error { return nil }

type mockDB struct {
	beginTxFunc func() (databases.Tx, error)
}

func (m *mockDB) BeginTx(_ context.Context, _ *sql.TxOptions) (databases.Tx, error) {
	return m.beginTxFunc()
}

func (m *mockDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return mockResult{}, nil
}

func (m *mockDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}

// --- Helpers ---

func newRetrier(maxRetries uint) *retrier.Retrier {
	return &retrier.Retrier{
		BackoffOptions: retrier.BackoffOptions{BackoffPolicy: zeroBackoff{}},
		MaxRetries:     maxRetries,
	}
}

func newTestExecutor(mdb *mockDB, maxInfraRetries uint) *StepExecutor {
	dbCtx := databases.NewDBContextWithDB(mdb, databases.SQLDialectPostgres)
	w := writer.New(dbCtx)
	exec, _ := New(mdb, w, newRetrier(maxInfraRetries))
	return exec
}

func okDB() *mockDB {
	return &mockDB{beginTxFunc: func() (databases.Tx, error) { return &mockTx{}, nil }}
}

func testStep(action step.Action, opts ...func(*step.StepParams)) *step.Step {
	if action == nil {
		action = func(_ context.Context, _ databases.TxQueryer, msg message.Message) (message.Message, error) {
			return msg, nil
		}
	}
	p := &step.StepParams{
		Name:    "test-step",
		Execute: action,
		Compensate: func(_ context.Context, _ databases.TxQueryer, msg message.Message) (message.Message, error) {
			return msg, nil
		},
		Routing: step.RoutingConfig{
			NextStepTopics: []string{"next-topic"},
			ErrorTopics:    []string{"error-topic"},
		},
	}
	for _, o := range opts {
		o(p)
	}
	s, _ := step.New(p)
	return s
}

func testMsg() message.Message {
	return message.Message{
		MessageMeta: message.MessageMeta{SagaID: testSagaID},
	}
}

func withOnError(h step.ErrorHandler) func(*step.StepParams) {
	return func(p *step.StepParams) { p.OnError = h }
}

func withRetryPolicy(r *retrier.Retrier) func(*step.StepParams) {
	return func(p *step.StepParams) { p.RetryPolicy = r }
}

func withCompensate(a step.Action) func(*step.StepParams) {
	return func(p *step.StepParams) { p.Compensate = a }
}

func withOnCompensateError(h step.ErrorHandler) func(*step.StepParams) {
	return func(p *step.StepParams) { p.OnCompensateError = h }
}

// --- Tests ---

func TestNew_Validation(t *testing.T) {
	mdb := okDB()
	dbCtx := databases.NewDBContextWithDB(mdb, databases.SQLDialectPostgres)
	w := writer.New(dbCtx)
	ir := newRetrier(3)

	tests := []struct {
		name string
		db   databases.DB
		w    *writer.Writer
		ir   *retrier.Retrier
		err  bool
	}{
		{"nil db", nil, w, ir, true},
		{"nil writer", mdb, nil, ir, true},
		{"nil infraRetrier", mdb, w, nil, false},
		{"all valid", mdb, w, ir, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.db, tt.w, tt.ir)
			if (err != nil) != tt.err {
				t.Errorf("got error=%v, wantErr=%v", err, tt.err)
			}
		})
	}
}

func TestExecuteStep_Success(t *testing.T) {
	var calls int32
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(func(_ context.Context, _ databases.TxQueryer, msg message.Message) (message.Message, error) {
		atomic.AddInt32(&calls, 1)
		return msg, nil
	})

	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("action called %d times, want 1", n)
	}
}

func TestExecuteStep_ActionFails_NoHandler_PublishesFailure(t *testing.T) {
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(func(_ context.Context, _ databases.TxQueryer, _ message.Message) (message.Message, error) {
		return message.Message{}, fmt.Errorf("business error")
	})

	// Failure event published to outbox → no error returned
	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success (failure event published), got: %v", err)
	}
}

func TestExecuteStep_RetryableError_RetriesWithNewTx(t *testing.T) {
	var actionCalls int32
	var txCount int32

	mdb := &mockDB{
		beginTxFunc: func() (databases.Tx, error) {
			atomic.AddInt32(&txCount, 1)
			return &mockTx{}, nil
		},
	}
	exec := newTestExecutor(mdb, 5)

	stp := testStep(
		func(_ context.Context, _ databases.TxQueryer, msg message.Message) (message.Message, error) {
			n := atomic.AddInt32(&actionCalls, 1)
			if n < 3 { // fail first 2
				return message.Message{}, retrier.AsRetryable(fmt.Errorf("transient"))
			}
			return msg, nil
		},
		withRetryPolicy(newRetrier(5)),
	)

	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success on 3rd retry, got: %v", err)
	}
	if n := atomic.LoadInt32(&actionCalls); n != 3 {
		t.Errorf("action called %d times, want 3", n)
	}
	// Each retry creates a new tx via atomicRun
	if n := atomic.LoadInt32(&txCount); n < 3 {
		t.Errorf("expected >= 3 txs, got %d", n)
	}
}

func TestExecuteStep_NonRetryableError_NoRetry(t *testing.T) {
	var actionCalls int32
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(
		func(_ context.Context, _ databases.TxQueryer, _ message.Message) (message.Message, error) {
			atomic.AddInt32(&actionCalls, 1)
			return message.Message{}, fmt.Errorf("permanent error") // NOT AsRetryable
		},
		withRetryPolicy(newRetrier(10)), // generous budget — should NOT be used
	)

	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success (failure published), got: %v", err)
	}
	if n := atomic.LoadInt32(&actionCalls); n != 1 {
		t.Errorf("action called %d times, want 1 (no retry for non-retryable)", n)
	}
}

func TestExecuteStep_InfraError_BeginTx_Retries(t *testing.T) {
	var beginCalls int32
	var actionCalls int32

	mdb := &mockDB{
		beginTxFunc: func() (databases.Tx, error) {
			n := atomic.AddInt32(&beginCalls, 1)
			if n <= 2 {
				return nil, fmt.Errorf("db unavailable")
			}
			return &mockTx{}, nil
		},
	}
	exec := newTestExecutor(mdb, 5)

	stp := testStep(func(_ context.Context, _ databases.TxQueryer, msg message.Message) (message.Message, error) {
		atomic.AddInt32(&actionCalls, 1)
		return msg, nil
	})

	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success after infra retry, got: %v", err)
	}
	if n := atomic.LoadInt32(&beginCalls); n < 3 {
		t.Errorf("expected >= 3 BeginTx calls, got %d", n)
	}
	if n := atomic.LoadInt32(&actionCalls); n != 1 {
		t.Errorf("action called %d times, want 1", n)
	}
}

func TestExecuteStep_InfraError_Commit_Retries(t *testing.T) {
	var actionCalls int32
	var txNum int32

	mdb := &mockDB{
		beginTxFunc: func() (databases.Tx, error) {
			n := atomic.AddInt32(&txNum, 1)
			if n == 1 {
				return &mockTx{commitErr: fmt.Errorf("connection lost")}, nil
			}
			return &mockTx{}, nil
		},
	}
	exec := newTestExecutor(mdb, 5)

	stp := testStep(func(_ context.Context, _ databases.TxQueryer, msg message.Message) (message.Message, error) {
		atomic.AddInt32(&actionCalls, 1)
		return msg, nil
	})

	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success after commit retry, got: %v", err)
	}
	// Action is called in each atomicRun attempt
	if n := atomic.LoadInt32(&actionCalls); n != 2 {
		t.Errorf("action called %d times, want 2 (1 commit fail + 1 success)", n)
	}
}

func TestExecuteStep_InfraDoesNotConsumeUserBudget(t *testing.T) {
	var txCount int32
	var actionCalls int32

	mdb := &mockDB{
		beginTxFunc: func() (databases.Tx, error) {
			n := atomic.AddInt32(&txCount, 1)
			if n <= 2 { // first 2 BeginTx calls fail (infra)
				return nil, fmt.Errorf("db unavailable")
			}
			return &mockTx{}, nil
		},
	}
	exec := newTestExecutor(mdb, 5) // infra budget=5

	stp := testStep(
		func(_ context.Context, _ databases.TxQueryer, msg message.Message) (message.Message, error) {
			n := atomic.AddInt32(&actionCalls, 1)
			if n <= 2 { // first 2 action calls fail with retryable
				return message.Message{}, retrier.AsRetryable(fmt.Errorf("transient"))
			}
			return msg, nil
		},
		withRetryPolicy(newRetrier(3)), // user budget=3
	)

	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	// Infra failures did NOT consume user retry budget
	if n := atomic.LoadInt32(&actionCalls); n != 3 {
		t.Errorf("action called %d times, want 3", n)
	}
}

func TestExecuteStep_WithErrorHandler_Succeeds(t *testing.T) {
	var handlerCalls int32
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(
		func(_ context.Context, _ databases.TxQueryer, _ message.Message) (message.Message, error) {
			return message.Message{}, fmt.Errorf("action failed")
		},
		withOnError(func(_ context.Context, _ databases.TxQueryer, msg message.Message, _ error) (message.Message, error) {
			atomic.AddInt32(&handlerCalls, 1)
			return msg, nil
		}),
	)

	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success (handler recovered), got: %v", err)
	}
	if n := atomic.LoadInt32(&handlerCalls); n != 1 {
		t.Errorf("handler called %d times, want 1", n)
	}
}

func TestExecuteStep_WithErrorHandler_Fails_PublishesFailure(t *testing.T) {
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(
		func(_ context.Context, _ databases.TxQueryer, _ message.Message) (message.Message, error) {
			return message.Message{}, fmt.Errorf("action failed")
		},
		withOnError(func(_ context.Context, _ databases.TxQueryer, msg message.Message, _ error) (message.Message, error) {
			return msg, fmt.Errorf("handler also failed")
		}),
	)

	// Both failed → failure event published → no error returned
	if err := exec.ExecuteStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success (failure published), got: %v", err)
	}
}

func TestExecuteStep_ContextCancelled(t *testing.T) {
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(
		func(_ context.Context, _ databases.TxQueryer, _ message.Message) (message.Message, error) {
			return message.Message{}, retrier.AsRetryable(fmt.Errorf("keep retrying"))
		},
		withRetryPolicy(newRetrier(100)),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := exec.ExecuteStep(ctx, stp, testMsg())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestCompensateStep_Success(t *testing.T) {
	var calls int32
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(nil, withCompensate(
		func(_ context.Context, _ databases.TxQueryer, msg message.Message) (message.Message, error) {
			atomic.AddInt32(&calls, 1)
			return msg, nil
		},
	))

	if err := exec.CompensateStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("compensate called %d times, want 1", n)
	}
}

func TestCompensateStep_Fails_NoHandler(t *testing.T) {
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(nil, withCompensate(
		func(_ context.Context, _ databases.TxQueryer, _ message.Message) (message.Message, error) {
			return message.Message{}, fmt.Errorf("compensate failed")
		},
	))

	err := exec.CompensateStep(context.Background(), stp, testMsg())
	if err == nil {
		t.Fatal("expected error for compensation failure without handler")
	}
}

func TestCompensateStep_WithHandler_Succeeds(t *testing.T) {
	var handlerCalls int32
	exec := newTestExecutor(okDB(), 3)

	stp := testStep(nil,
		withCompensate(func(_ context.Context, _ databases.TxQueryer, _ message.Message) (message.Message, error) {
			return message.Message{}, fmt.Errorf("compensate failed")
		}),
		withOnCompensateError(func(_ context.Context, _ databases.TxQueryer, msg message.Message, _ error) (message.Message, error) {
			atomic.AddInt32(&handlerCalls, 1)
			return msg, nil
		}),
	)

	if err := exec.CompensateStep(context.Background(), stp, testMsg()); err != nil {
		t.Fatalf("expected success (handler recovered), got: %v", err)
	}
	if n := atomic.LoadInt32(&handlerCalls); n != 1 {
		t.Errorf("handler called %d times, want 1", n)
	}
}

func TestActionError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("inner error")
	ae := &actionError{err: inner}

	if ae.Error() != "inner error" {
		t.Errorf("Error() = %q, want %q", ae.Error(), "inner error")
	}
	if !errors.Is(ae, inner) {
		t.Error("Unwrap should return inner error")
	}
}
