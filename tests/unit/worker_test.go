package unit

import (
	"strings"
	"testing"
	"time"

	"github.com/Lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/stretchr/testify/assert"
)

func TestGenerateWorkerID_Format(t *testing.T) {
	// Generate multiple IDs
	ids := make(map[string]bool)
	for i := 0; i < 10; i++ {
		// We'll test the public NewWorker function which generates WorkerID
		worker := bullmq.NewWorker("test-queue", nil, bullmq.WorkerOptions{})
		id := worker.GetWorkerID() // We need to add this getter

		// Verify format: {hostname}-{pid}-{random6}
		parts := strings.Split(id, "-")
		assert.GreaterOrEqual(t, len(parts), 3,
			"WorkerID should have at least 3 parts: hostname-pid-random")

		// Last part should be 6-character hex
		randomPart := parts[len(parts)-1]
		assert.Len(t, randomPart, 6,
			"Random part should be 6 characters")

		// Store for uniqueness check
		ids[id] = true
	}

	// All IDs should be unique
	assert.Len(t, ids, 10,
		"All generated WorkerIDs should be unique")
}

func TestWorkerOptions_Defaults(t *testing.T) {
	defaults := bullmq.DefaultWorkerOptions

	assert.Equal(t, 1, defaults.Concurrency)
	assert.Equal(t, 30*time.Second, defaults.LockDuration)
	assert.Equal(t, 15*time.Second, defaults.HeartbeatInterval)
	assert.Equal(t, 30*time.Second, defaults.StalledCheckInterval)
	assert.Equal(t, 3, defaults.MaxAttempts)
	assert.Equal(t, 1*time.Second, defaults.BackoffDelay)
	assert.Equal(t, 1*time.Hour, defaults.MaxBackoffDelay)
	assert.Equal(t, "", defaults.WorkerID)
	assert.Equal(t, 0, defaults.MaxReconnectAttempts)
	assert.Equal(t, int64(10000), defaults.EventsMaxLen)
	assert.Equal(t, 30*time.Second, defaults.ShutdownTimeout)
}
