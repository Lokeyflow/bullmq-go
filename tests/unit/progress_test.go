package unit

import (
	"testing"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/stretchr/testify/assert"
)

func TestJob_UpdateProgress_ValidRange(t *testing.T) {
	job := &bullmq.Job{ID: "test-job"}

	err := job.UpdateProgress(0)
	assert.NoError(t, err)
	assert.Equal(t, 0, job.Progress)

	err = job.UpdateProgress(50)
	assert.NoError(t, err)
	assert.Equal(t, 50, job.Progress)

	err = job.UpdateProgress(100)
	assert.NoError(t, err)
	assert.Equal(t, 100, job.Progress)
}

func TestJob_UpdateProgress_InvalidRange(t *testing.T) {
	job := &bullmq.Job{ID: "test-job"}

	err := job.UpdateProgress(-1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must be between 0 and 100")

	err = job.UpdateProgress(101)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must be between 0 and 100")
}

func TestJob_Log_NoRedisClient(t *testing.T) {
	job := &bullmq.Job{ID: "test-job"}

	// Should not error when Redis client is not set
	err := job.Log("test message")
	assert.NoError(t, err)
}
