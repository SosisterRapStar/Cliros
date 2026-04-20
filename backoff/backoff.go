package backoff

import (
	"math"
	"math/rand"
	"time"
)

const defaultExpFactor = 2.0

type BackoffPolicy interface {
	CalcBackoff(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration
}

// Expontential реализует экспоненциальный backoff: minBackoff * factor^(retryNumber-1).
// Если ExpFactor <= 1 — используется значение по умолчанию (2.0).
// Для retryNumber == 0 возвращается minBackoff.
// Если maxBackoff > 0 — результат ограничивается сверху maxBackoff.
type Expontential struct {
	ExpFactor float64
}

func NewExpontential(factor float64) Expontential {
	if factor <= 1 {
		factor = defaultExpFactor
	}
	return Expontential{ExpFactor: factor}
}

func (e Expontential) CalcBackoff(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration {
	if retryNumber == 0 {
		return minBackoff
	}

	factor := e.ExpFactor
	if factor <= 1 {
		factor = defaultExpFactor
	}

	base := float64(minBackoff)
	if base <= 0 {
		base = 1
	}

	backoff := base * math.Pow(factor, float64(retryNumber-1))
	if maxBackoff > 0 && backoff > float64(maxBackoff) {
		return maxBackoff
	}
	return time.Duration(backoff)
}

// ExpontentialWithJitter — экспоненциальный backoff с джиттером:
// значение выбирается случайно в диапазоне [minBackoff, backoff], где
// backoff = minBackoff * factor^(retryNumber-1), ограниченный maxBackoff.
type ExpontentialWithJitter struct {
	ExpFactor float64
}

func NewExpontentialWithJitter(factor float64) ExpontentialWithJitter {
	if factor <= 1 {
		factor = defaultExpFactor
	}
	return ExpontentialWithJitter{ExpFactor: factor}
}

func (e ExpontentialWithJitter) CalcBackoff(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration {
	if retryNumber == 0 {
		return minBackoff
	}

	factor := e.ExpFactor
	if factor <= 1 {
		factor = defaultExpFactor
	}

	base := float64(minBackoff)
	if base <= 0 {
		base = 1
	}

	cappedBackoff := base * math.Pow(factor, float64(retryNumber-1))
	if maxBackoff > 0 && cappedBackoff > float64(maxBackoff) {
		cappedBackoff = float64(maxBackoff)
	}

	jittered := rand.Float64() * cappedBackoff //nolint:gosec

	result := math.Max(jittered, float64(minBackoff))

	return time.Duration(math.Ceil(result))
}
