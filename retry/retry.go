package retry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SosisterRapStar/cliros/backoff"
)

var (
	ErrMaxRetriesExceeded = errors.New("max retries number achieved")
)

type RetryableError struct {
	err error
}

func (r *RetryableError) Error() string {
	return r.err.Error()
}

func (r *RetryableError) Unwrap() error {
	return r.err
}

func AsRetryable(err error) *RetryableError {
	return &RetryableError{err: err}
}

func NewRetryable(err string) *RetryableError {
	return &RetryableError{err: errors.New(err)}
}

type work func(ctx context.Context) error

type BackoffOptions struct {
	BackoffPolicy backoff.BackoffPolicy
	MinBackoff    time.Duration
	MaxBackoff    time.Duration
}

type Retrier struct {
	BackoffOptions
	MaxRetries uint
}

func (r *Retrier) Retry(ctx context.Context, work work) error {
	return r.retry(ctx, work)
}

func (r *Retrier) retry(ctx context.Context, work work) error {
	var (
		retryable *RetryableError
		retries   uint
		lastErr   error
	)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if retries >= r.MaxRetries {
				if lastErr != nil {
					return fmt.Errorf("%w: %w", ErrMaxRetriesExceeded, lastErr)
				}
				return ErrMaxRetriesExceeded
			}
			err := work(ctx)
			if err == nil {
				return nil
			}
			if !errors.As(err, &retryable) {
				return err
			}
			lastErr = err
			err = sleep(ctx,
				r.BackoffPolicy.
					CalcBackoff(retries,
						r.MinBackoff,
						r.MaxBackoff,
					),
			)
			if err != nil {
				return err
			}
			retries++
		}
	}
}

func sleep(ctx context.Context, sleepTime time.Duration) error {
	select {
	case <-time.After(sleepTime):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
