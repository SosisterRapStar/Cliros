package step

// пока не определился, как делать лучше
// делать такие ошибки или дать пользователю делать хэндлеры onError

// простая retry политика

// для этой штуки нужен интерфейс
// чтобы пользователь мог делать свои retry policy

// type RetryPolicy struct {
// 	MaxAttempts     int
// 	InitialDelay    time.Duration
// 	MaxDelay        time.Duration
// 	Multiplier      float64
// 	RetryableErrors []string
// }

// func DefaultRetryPolicy() RetryPolicy {
// 	return RetryPolicy{
// 		MaxAttempts:     3,
// 		InitialDelay:    1 * time.Second,
// 		MaxDelay:        30 * time.Second,
// 		Multiplier:      2.0,
// 		RetryableErrors: []string{},
// 	}
// }

// func (p RetryPolicy) CalculateDelay(attempt int) time.Duration {
// 	if attempt <= 0 {
// 		return p.InitialDelay
// 	}

// 	delay := time.Duration(float64(p.InitialDelay) * math.Pow(p.Multiplier, float64(attempt-1)))
// 	if delay > p.MaxDelay {
// 		return p.MaxDelay
// 	}

// 	return delay
// }

// type RetryableError struct {
// 	wrapped error
// }

// func (r *RetryableError) Error() string {
// 	if r.wrapped == nil {
// 		return "retryable error"
// 	}
// 	return r.wrapped.Error()
// }

// func (r *RetryableError) Unwrap() error {
// 	return r.wrapped
// }

// func WrapRetryable(err error) error {
// 	return &RetryableError{wrapped: err}
// }

// func IsRetryable(err error) bool {
// 	var retryableErr *RetryableError
// 	return errors.As(err, &retryableErr)
// }
