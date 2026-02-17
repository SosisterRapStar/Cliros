package backoff

import (
	"testing"
	"time"
)

func TestExpontential_CalcBackoff(t *testing.T) {
	t.Run("returns minBackoff for retryNumber <= 0", func(t *testing.T) {
		policy := Expontential{expFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 100 * time.Millisecond

		result := policy.CalcBackoff(0, minBackoff, maxBackoff)
		if result != minBackoff {
			t.Errorf("expected %v, got %v", minBackoff, result)
		}
	})

	t.Run("increases backoff with retry number", func(t *testing.T) {
		policy := Expontential{expFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 100 * time.Millisecond

		result1 := policy.CalcBackoff(1, minBackoff, maxBackoff)
		result2 := policy.CalcBackoff(2, minBackoff, maxBackoff)
		result3 := policy.CalcBackoff(3, minBackoff, maxBackoff)

		// retryNumber=1: 1 + 0*2 = 1ms -> ceil(1) = 1ms, но ограничено minBackoff
		// retryNumber=2: 1 + 1*2 = 3ms -> ceil(3) = 3ms
		// retryNumber=3: 1 + 2*2 = 5ms -> ceil(5) = 5ms
		if result2 <= result1 {
			t.Errorf("expected result2 > result1, got result1=%v, result2=%v", result1, result2)
		}
		if result3 <= result2 {
			t.Errorf("expected result3 > result2, got result2=%v, result3=%v", result2, result3)
		}
	})

	t.Run("respects maxBackoff limit", func(t *testing.T) {
		policy := Expontential{expFactor: 100.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 50 * time.Millisecond

		result := policy.CalcBackoff(10, minBackoff, maxBackoff)
		if result > maxBackoff {
			t.Errorf("expected result <= %v, got %v", maxBackoff, result)
		}
	})

	t.Run("works without maxBackoff limit", func(t *testing.T) {
		policy := Expontential{expFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		var maxBackoff time.Duration = 0

		result := policy.CalcBackoff(5, minBackoff, maxBackoff)
		// Без maxBackoff результат может быть любым (формула: 1 + (retryNumber-1)*expFactor)
		// retryNumber=5: 1 + 4*2 = 9 -> ceil(9) = 9ns
		if result <= 0 {
			t.Errorf("expected positive result, got %v", result)
		}
	})
}

func TestExpontentialWithJitter_CalcBackoff(t *testing.T) {
	t.Run("returns minBackoff for retryNumber <= 0", func(t *testing.T) {
		policy := ExpontentialWithJitter{expFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 100 * time.Millisecond

		result := policy.CalcBackoff(0, minBackoff, maxBackoff)
		if result != minBackoff {
			t.Errorf("expected %v, got %v", minBackoff, result)
		}
	})

	t.Run("result is within expected range", func(t *testing.T) {
		policy := ExpontentialWithJitter{expFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 100 * time.Millisecond

		// Делаем несколько попыток, чтобы проверить что результат всегда в диапазоне
		for i := 0; i < 10; i++ {
			result := policy.CalcBackoff(3, minBackoff, maxBackoff)
			if result < minBackoff {
				t.Errorf("result %v is less than minBackoff %v", result, minBackoff)
			}
			if result > maxBackoff {
				t.Errorf("result %v is greater than maxBackoff %v", result, maxBackoff)
			}
		}
	})

	t.Run("respects maxBackoff limit", func(t *testing.T) {
		policy := ExpontentialWithJitter{expFactor: 100.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 50 * time.Millisecond

		// Делаем несколько попыток
		for i := 0; i < 10; i++ {
			result := policy.CalcBackoff(10, minBackoff, maxBackoff)
			if result > maxBackoff {
				t.Errorf("result %v exceeds maxBackoff %v", result, maxBackoff)
			}
			if result < minBackoff {
				t.Errorf("result %v is less than minBackoff %v", result, minBackoff)
			}
		}
	})
}
