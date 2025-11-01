package unit

import (
	"strings"
	"testing"
	"time"

	"github.com/Lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/stretchr/testify/assert"
)

func TestValidateJobOptions_ValidOptions(t *testing.T) {
	opts := bullmq.JobOptions{
		Priority: 10,
		Delay:    5 * time.Second,
		Attempts: 3,
		Backoff: bullmq.BackoffConfig{
			Type:  "exponential",
			Delay: 1000,
		},
	}

	err := bullmq.ValidateJobOptions(opts)
	assert.NoError(t, err)
}

func TestValidateJobOptions_NegativePriority(t *testing.T) {
	opts := bullmq.JobOptions{
		Priority: -10,
		Attempts: 3,
		Backoff:  bullmq.BackoffConfig{Type: "exponential", Delay: 1000},
	}

	err := bullmq.ValidateJobOptions(opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "priority")
	assert.Contains(t, err.Error(), "must be >= 0")
}

func TestValidateJobOptions_NegativeDelay(t *testing.T) {
	opts := bullmq.JobOptions{
		Delay:    -5 * time.Second,
		Attempts: 3,
		Backoff:  bullmq.BackoffConfig{Type: "exponential", Delay: 1000},
	}

	err := bullmq.ValidateJobOptions(opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delay")
}

func TestValidateJobOptions_ZeroAttempts(t *testing.T) {
	opts := bullmq.JobOptions{
		Attempts: 0,
		Backoff:  bullmq.BackoffConfig{Type: "exponential", Delay: 1000},
	}

	err := bullmq.ValidateJobOptions(opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "attempts")
	assert.Contains(t, err.Error(), "must be > 0")
}

func TestValidateBackoffConfig_InvalidType(t *testing.T) {
	backoff := bullmq.BackoffConfig{
		Type:  "invalid",
		Delay: 1000,
	}

	err := bullmq.ValidateBackoffConfig(backoff)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "backoff.type")
	assert.Contains(t, err.Error(), "must be 'fixed' or 'exponential'")
}

func TestValidateBackoffConfig_ZeroDelay(t *testing.T) {
	backoff := bullmq.BackoffConfig{
		Type:  "exponential",
		Delay: 0,
	}

	err := bullmq.ValidateBackoffConfig(backoff)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "backoff.delay")
}

func TestValidateJobPayloadSize_ValidSize(t *testing.T) {
	job := &bullmq.Job{
		ID:   "test-job",
		Name: "test",
		Data: map[string]interface{}{
			"message": "Hello, World!",
		},
	}

	err := bullmq.ValidateJobPayloadSize(job)
	assert.NoError(t, err)
}

func TestValidateJobPayloadSize_ExceedsLimit(t *testing.T) {
	// Create job with >10MB payload
	largeData := make(map[string]interface{})
	largeData["bigString"] = strings.Repeat("x", 11*1024*1024) // 11MB

	job := &bullmq.Job{
		ID:   "large-job",
		Name: "test",
		Data: largeData,
	}

	err := bullmq.ValidateJobPayloadSize(job)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds limit")
	assert.Contains(t, err.Error(), "10")
}
