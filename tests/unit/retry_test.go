package unit

import (
	"testing"
	"time"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/stretchr/testify/assert"
)

func TestCalculateBackoff_ExponentialGrowth(t *testing.T) {
	initialDelay := 1 * time.Second
	maxDelay := 1 * time.Hour

	tests := []struct {
		attemptsMade int
		minExpected  time.Duration
		maxExpected  time.Duration
	}{
		{1, 800 * time.Millisecond, 1200 * time.Millisecond},   // 1s ± 20%
		{2, 1600 * time.Millisecond, 2400 * time.Millisecond},  // 2s ± 20%
		{3, 3200 * time.Millisecond, 4800 * time.Millisecond},  // 4s ± 20%
		{4, 6400 * time.Millisecond, 9600 * time.Millisecond},  // 8s ± 20%
		{5, 12800 * time.Millisecond, 19200 * time.Millisecond}, // 16s ± 20%
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.attemptsMade)), func(t *testing.T) {
			delay := bullmq.CalculateBackoff(tt.attemptsMade, initialDelay, maxDelay)

			assert.GreaterOrEqual(t, delay, tt.minExpected,
				"Delay for attempt %d should be >= %v", tt.attemptsMade, tt.minExpected)
			assert.LessOrEqual(t, delay, tt.maxExpected,
				"Delay for attempt %d should be <= %v", tt.attemptsMade, tt.maxExpected)
		})
	}
}

func TestCalculateBackoff_MaxCap(t *testing.T) {
	initialDelay := 1 * time.Second
	maxDelay := 10 * time.Second

	// Attempt 10 would normally be 512s, but capped at 10s
	delay := bullmq.CalculateBackoff(10, initialDelay, maxDelay)
	assert.LessOrEqual(t, delay, maxDelay,
		"Delay should be capped at max delay")
}

func TestCalculateBackoff_FirstAttempt(t *testing.T) {
	initialDelay := 2 * time.Second
	maxDelay := 1 * time.Hour

	delay := bullmq.CalculateBackoff(1, initialDelay, maxDelay)

	// First attempt should be close to initial delay with jitter
	assert.GreaterOrEqual(t, delay, 1600*time.Millisecond) // 2s * 0.8
	assert.LessOrEqual(t, delay, 2400*time.Millisecond)    // 2s * 1.2
}

func TestCalculateBackoff_ZeroAttempts(t *testing.T) {
	initialDelay := 1 * time.Second
	maxDelay := 1 * time.Hour

	delay := bullmq.CalculateBackoff(0, initialDelay, maxDelay)
	assert.Equal(t, initialDelay, delay,
		"Zero attempts should return initial delay")
}

func TestCalculateBackoff_OneHourCap(t *testing.T) {
	initialDelay := 1 * time.Second
	maxDelay := 1 * time.Hour

	// Attempt 12+ should be capped at 1 hour
	for attempt := 12; attempt <= 20; attempt++ {
		delay := bullmq.CalculateBackoff(attempt, initialDelay, maxDelay)
		assert.LessOrEqual(t, delay, maxDelay,
			"Attempt %d should be capped at 1 hour", attempt)
	}
}
