package backoff

import (
	"testing"
	"time"
)

func TestExpontential_CalcBackoff(t *testing.T) {
	t.Run("returns minBackoff for retryNumber == 0", func(t *testing.T) {
		policy := Expontential{ExpFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 100 * time.Millisecond

		result := policy.CalcBackoff(0, minBackoff, maxBackoff)
		if result != minBackoff {
			t.Errorf("expected %v, got %v", minBackoff, result)
		}
	})

	t.Run("increases backoff with retry number", func(t *testing.T) {
		policy := Expontential{ExpFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 10 * time.Second

		result1 := policy.CalcBackoff(1, minBackoff, maxBackoff)
		result2 := policy.CalcBackoff(2, minBackoff, maxBackoff)
		result3 := policy.CalcBackoff(3, minBackoff, maxBackoff)

		if result1 != minBackoff {
			t.Errorf("expected result1 == minBackoff (%v), got %v", minBackoff, result1)
		}
		if result2 <= result1 {
			t.Errorf("expected result2 > result1, got result1=%v, result2=%v", result1, result2)
		}
		if result3 <= result2 {
			t.Errorf("expected result3 > result2, got result2=%v, result3=%v", result2, result3)
		}
	})

	t.Run("respects maxBackoff limit", func(t *testing.T) {
		policy := Expontential{ExpFactor: 100.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 50 * time.Millisecond

		result := policy.CalcBackoff(10, minBackoff, maxBackoff)
		if result > maxBackoff {
			t.Errorf("expected result <= %v, got %v", maxBackoff, result)
		}
	})

	t.Run("works without maxBackoff limit", func(t *testing.T) {
		policy := Expontential{ExpFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		var maxBackoff time.Duration = 0

		result := policy.CalcBackoff(5, minBackoff, maxBackoff)
		if result <= 0 {
			t.Errorf("expected positive result, got %v", result)
		}
	})

	t.Run("defaults expFactor when zero", func(t *testing.T) {
		policy := Expontential{}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 10 * time.Second

		result1 := policy.CalcBackoff(1, minBackoff, maxBackoff)
		result2 := policy.CalcBackoff(2, minBackoff, maxBackoff)

		if result1 < minBackoff {
			t.Errorf("expected result1 >= minBackoff (%v), got %v", minBackoff, result1)
		}
		if result2 <= result1 {
			t.Errorf("expected growth with default factor, got result1=%v, result2=%v", result1, result2)
		}
	})

	t.Run("NewExpontential applies default for invalid factor", func(t *testing.T) {
		policy := NewExpontential(0)
		if policy.ExpFactor != defaultExpFactor {
			t.Errorf("expected default factor %v, got %v", defaultExpFactor, policy.ExpFactor)
		}
	})
}

func TestExpontentialWithJitter_CalcBackoff(t *testing.T) {
	t.Run("returns minBackoff for retryNumber == 0", func(t *testing.T) {
		policy := ExpontentialWithJitter{ExpFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 100 * time.Millisecond

		result := policy.CalcBackoff(0, minBackoff, maxBackoff)
		if result != minBackoff {
			t.Errorf("expected %v, got %v", minBackoff, result)
		}
	})

	t.Run("result is within expected range", func(t *testing.T) {
		policy := ExpontentialWithJitter{ExpFactor: 2.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 100 * time.Millisecond

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
		policy := ExpontentialWithJitter{ExpFactor: 100.0}
		minBackoff := 10 * time.Millisecond
		maxBackoff := 50 * time.Millisecond

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
