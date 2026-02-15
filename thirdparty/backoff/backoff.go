package backoff

import (
	"math"
	"time"
)

type BackoffPolicy interface {
	CalcBackoff(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration
}

type Expontential struct {
	expFactor float64
}

func (e Expontential) CalcBackoff(retryNumber uint, minBackoff, maxBackoff time.Duration) time.Duration {
	var (
		nillDur time.Duration
	)
	if retryNumber <= 0 {
		return minBackoff
	}
	newBackoff := 1.0 + float64(retryNumber-1)*e.expFactor

	// если плато бэкоффа не указано - то увеличиваем бесконечно
	if maxBackoff != nillDur {
		return time.Duration(math.Ceil(min(newBackoff, float64(maxBackoff))))
	} else {
		return time.Duration(math.Ceil(newBackoff))
	}
}
