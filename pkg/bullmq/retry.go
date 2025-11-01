package bullmq

import (
	"math"
	"math/rand"
	"time"
)

// CalculateBackoff calculates retry delay with exponential backoff and jitter
// Formula: min(initialDelay * 2^(attemptsMade-1) * jitter, maxDelay)
// Jitter: ±20% (0.8 to 1.2)
func CalculateBackoff(attemptsMade int, initialDelay, maxDelay time.Duration) time.Duration {
	if attemptsMade < 1 {
		return initialDelay
	}

	// Exponential backoff: initialDelay * 2^(attemptsMade-1)
	exponent := attemptsMade - 1
	baseDelay := float64(initialDelay) * math.Pow(2, float64(exponent))

	// Apply jitter (±20%): random between 0.8 and 1.2
	jitter := 0.8 + 0.4*rand.Float64()
	delay := time.Duration(baseDelay * jitter)

	// Cap at max delay
	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}
