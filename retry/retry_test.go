package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockBackoffPolicy - мок для тестирования, возвращает фиксированную задержку
type mockBackoffPolicy struct {
	backoff time.Duration
}

func (m *mockBackoffPolicy) CalcBackoff(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration {
	return m.backoff
}

func TestRetryableError(t *testing.T) {
	t.Run("Error returns wrapped error message", func(t *testing.T) {
		originalErr := errors.New("original error")
		retryableErr := AsRetryable(originalErr)

		if retryableErr.Error() != "original error" {
			t.Errorf("expected 'original error', got '%s'", retryableErr.Error())
		}
	})

	t.Run("Unwrap returns original error", func(t *testing.T) {
		originalErr := errors.New("original error")
		retryableErr := AsRetryable(originalErr)

		unwrapped := retryableErr.Unwrap()
		if unwrapped != originalErr {
			t.Errorf("expected original error, got %v", unwrapped)
		}
	})

	t.Run("AsRetryable wraps error", func(t *testing.T) {
		originalErr := errors.New("test error")
		retryableErr := AsRetryable(originalErr)

		if retryableErr == nil {
			t.Fatal("expected non-nil RetryableError")
		}

		var retryable *RetryableError
		if !errors.As(retryableErr, &retryable) {
			t.Error("expected error to be RetryableError")
		}
	})

	t.Run("NewRetryable creates RetryableError from string", func(t *testing.T) {
		retryableErr := NewRetryable("test error")

		if retryableErr == nil {
			t.Fatal("expected non-nil RetryableError")
		}

		if retryableErr.Error() != "test error" {
			t.Errorf("expected 'test error', got '%s'", retryableErr.Error())
		}
	})
}

func TestRetrier_Retry(t *testing.T) { //nolint: gocyclo
	t.Run("success on first attempt", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: &mockBackoffPolicy{backoff: 10 * time.Millisecond},
				MinBackoff:    10 * time.Millisecond,
				MaxBackoff:    100 * time.Millisecond,
			},
			MaxRetries: 3,
		}

		attempts := 0
		err := retrier.Retry(context.Background(), func(ctx context.Context) error {
			attempts++
			return nil
		})

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
	})

	t.Run("success after retries", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: &mockBackoffPolicy{backoff: 10 * time.Millisecond},
				MinBackoff:    10 * time.Millisecond,
				MaxBackoff:    100 * time.Millisecond,
			},
			MaxRetries: 5,
		}

		attempts := 0
		err := retrier.Retry(context.Background(), func(ctx context.Context) error {
			attempts++
			if attempts < 3 {
				return AsRetryable(errors.New("temporary error"))
			}
			return nil
		})

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("max retries exceeded", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: &mockBackoffPolicy{backoff: 10 * time.Millisecond},
				MinBackoff:    10 * time.Millisecond,
				MaxBackoff:    100 * time.Millisecond,
			},
			MaxRetries: 3,
		}

		attempts := 0
		err := retrier.Retry(context.Background(), func(ctx context.Context) error {
			attempts++
			return AsRetryable(errors.New("always fails"))
		})

		if !errors.Is(err, ErrMaxRetriesExceeded) {
			t.Errorf("expected MaxRetriesExceeded, got %v", err)
		}
		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
	})

	t.Run("non-retryable error stops immediately", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: &mockBackoffPolicy{backoff: 10 * time.Millisecond},
				MinBackoff:    10 * time.Millisecond,
				MaxBackoff:    100 * time.Millisecond,
			},
			MaxRetries: 5,
		}

		nonRetryableErr := errors.New("permanent error")
		attempts := 0
		err := retrier.Retry(context.Background(), func(ctx context.Context) error {
			attempts++
			return nonRetryableErr
		})

		if !errors.Is(err, nonRetryableErr) {
			t.Errorf("expected permanent error, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
	})

	t.Run("context cancellation stops retry", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: &mockBackoffPolicy{backoff: 100 * time.Millisecond},
				MinBackoff:    100 * time.Millisecond,
				MaxBackoff:    200 * time.Millisecond,
			},
			MaxRetries: 10,
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // отменяем сразу

		attempts := 0
		err := retrier.Retry(ctx, func(ctx context.Context) error {
			attempts++
			return AsRetryable(errors.New("always fails"))
		})

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		if attempts != 0 {
			t.Errorf("expected 0 attempts (canceled before first), got %d", attempts)
		}
	})

	t.Run("context cancellation during backoff", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: &mockBackoffPolicy{backoff: 200 * time.Millisecond},
				MinBackoff:    200 * time.Millisecond,
				MaxBackoff:    300 * time.Millisecond,
			},
			MaxRetries: 5,
		}

		ctx, cancel := context.WithCancel(context.Background())

		attempts := 0
		go func() {
			time.Sleep(50 * time.Millisecond) // отменяем во время backoff
			cancel()
		}()

		err := retrier.Retry(ctx, func(ctx context.Context) error {
			attempts++
			return AsRetryable(errors.New("always fails"))
		})

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		if attempts < 1 {
			t.Errorf("expected at least 1 attempt, got %d", attempts)
		}
	})

	t.Run("context timeout stops retry", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: &mockBackoffPolicy{backoff: 100 * time.Millisecond},
				MinBackoff:    100 * time.Millisecond,
				MaxBackoff:    200 * time.Millisecond,
			},
			MaxRetries: 10,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		attempts := 0
		err := retrier.Retry(ctx, func(ctx context.Context) error {
			attempts++
			return AsRetryable(errors.New("always fails"))
		})

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("zero max retries fails immediately", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: &mockBackoffPolicy{backoff: 10 * time.Millisecond},
				MinBackoff:    10 * time.Millisecond,
				MaxBackoff:    100 * time.Millisecond,
			},
			MaxRetries: 0,
		}

		attempts := 0
		err := retrier.Retry(context.Background(), func(ctx context.Context) error {
			attempts++
			return AsRetryable(errors.New("always fails"))
		})

		if !errors.Is(err, ErrMaxRetriesExceeded) {
			t.Errorf("expected MaxRetriesExceeded, got %v", err)
		}
		if attempts != 0 {
			t.Errorf("expected 0 attempts, got %d", attempts)
		}
	})

	t.Run("backoff policy is called with correct parameters", func(t *testing.T) {
		callCount := 0
		lastRetryNumber := uint(0)
		lastMinBackoff := time.Duration(0)
		lastMaxBackoff := time.Duration(0)

		mockPolicy := &backoffPolicySpy{
			calcBackoff: func(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration {
				callCount++
				lastRetryNumber = retryNumber
				lastMinBackoff = minBackoff
				lastMaxBackoff = maxBackoff
				return 10 * time.Millisecond
			},
		}

		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: mockPolicy,
				MinBackoff:    50 * time.Millisecond,
				MaxBackoff:    200 * time.Millisecond,
			},
			MaxRetries: 3,
		}

		attempts := 0
		_ = retrier.Retry(context.Background(), func(ctx context.Context) error {
			attempts++
			if attempts < 3 {
				return AsRetryable(errors.New("temporary error"))
			}
			return nil
		})

		// Backoff должен вызываться после каждой неудачной попытки
		// Попытка 1 -> retryNumber=0, Попытка 2 -> retryNumber=1
		if callCount != 2 {
			t.Errorf("expected 2 backoff calls, got %d", callCount)
		}
		if lastRetryNumber != 1 {
			t.Errorf("expected last retryNumber=1, got %d", lastRetryNumber)
		}
		if lastMinBackoff != 50*time.Millisecond {
			t.Errorf("expected minBackoff=50ms, got %v", lastMinBackoff)
		}
		if lastMaxBackoff != 200*time.Millisecond {
			t.Errorf("expected maxBackoff=200ms, got %v", lastMaxBackoff)
		}
	})
}

func TestSleep(t *testing.T) {
	t.Run("sleep completes successfully", func(t *testing.T) {
		ctx := context.Background()
		start := time.Now()
		err := sleep(ctx, 50*time.Millisecond)
		duration := time.Since(start)

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if duration < 50*time.Millisecond {
			t.Errorf("expected sleep duration >= 50ms, got %v", duration)
		}
	})

	t.Run("sleep is canceled by context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		start := time.Now()
		err := sleep(ctx, 200*time.Millisecond)
		duration := time.Since(start)

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		if duration >= 200*time.Millisecond {
			t.Errorf("expected sleep to be canceled quickly, got %v", duration)
		}
	})

	t.Run("sleep is canceled by timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := sleep(ctx, 200*time.Millisecond)
		duration := time.Since(start)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %v", err)
		}
		if duration >= 200*time.Millisecond {
			t.Errorf("expected sleep to be canceled quickly, got %v", duration)
		}
	})
}

// backoffPolicySpy - spy для отслеживания вызовов BackoffPolicy
type backoffPolicySpy struct {
	calcBackoff func(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration
}

func (b *backoffPolicySpy) CalcBackoff(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration {
	return b.calcBackoff(retryNumber, minBackoff, maxBackoff)
}

// exponentialBackoffPolicy - тестовая реализация экспоненциального backoff
type exponentialBackoffPolicy struct {
	expFactor float64
}

func (e exponentialBackoffPolicy) CalcBackoff(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration {
	if retryNumber <= 0 {
		return minBackoff
	}
	newBackoff := 1.0 + float64(retryNumber-1)*e.expFactor

	var maxBackoffFloat float64
	if maxBackoff != 0 {
		maxBackoffFloat = float64(maxBackoff)
		if newBackoff > maxBackoffFloat {
			return maxBackoff
		}
	}
	// Применяем minBackoff как минимум
	result := time.Duration(newBackoff)
	if result < minBackoff {
		return minBackoff
	}
	return result
}

// Интеграционный тест с экспоненциальным backoff
func TestRetrier_WithExponentialBackoff(t *testing.T) {
	t.Run("exponential backoff increases delay", func(t *testing.T) {
		retrier := &Retrier{
			BackoffOptions: BackoffOptions{
				BackoffPolicy: exponentialBackoffPolicy{expFactor: 2.0},
				MinBackoff:    10 * time.Millisecond,
				MaxBackoff:    100 * time.Millisecond,
			},
			MaxRetries: 3,
		}

		attempts := 0
		start := time.Now()
		err := retrier.Retry(context.Background(), func(ctx context.Context) error {
			attempts++
			if attempts < 3 {
				return AsRetryable(errors.New("temporary error"))
			}
			return nil
		})
		duration := time.Since(start)

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if attempts != 3 {
			t.Errorf("expected 3 attempts, got %d", attempts)
		}
		// Проверяем, что была задержка между попытками
		if duration < 5*time.Millisecond {
			t.Errorf("expected some backoff delay, got %v", duration)
		}
	})
}
