package executor

// actionError — обёртка ошибки пользовательского action.
// infraRetrier видит её как НЕ-RetryableError и останавливается.
// Внешний user-retry цикл разворачивает её и проверяет inner error.
type actionError struct {
	err error
}

func (e *actionError) Error() string {
	return e.err.Error()
}

func (e *actionError) Unwrap() error {
	return e.err
}
