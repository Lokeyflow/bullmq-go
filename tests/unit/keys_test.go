package unit

import (
	"strings"
	"testing"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/stretchr/testify/assert"
)

func TestKeyBuilder_HashTags(t *testing.T) {
	// Test with hash tags enabled (cluster mode)
	kb := bullmq.NewKeyBuilderWithHashTags("myqueue", true)

	tests := []struct {
		name     string
		keyFunc  func() string
		expected string
	}{
		{"Wait", kb.Wait, "bull:{myqueue}:wait"},
		{"Prioritized", kb.Prioritized, "bull:{myqueue}:prioritized"},
		{"Delayed", kb.Delayed, "bull:{myqueue}:delayed"},
		{"Active", kb.Active, "bull:{myqueue}:active"},
		{"Completed", kb.Completed, "bull:{myqueue}:completed"},
		{"Failed", kb.Failed, "bull:{myqueue}:failed"},
		{"Events", kb.Events, "bull:{myqueue}:events"},
		{"Meta", kb.Meta, "bull:{myqueue}:meta"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := tt.keyFunc()
			assert.Equal(t, tt.expected, key)

			// Verify hash tag format: {queue-name}
			assert.Contains(t, key, "{myqueue}")
			assert.True(t, strings.HasPrefix(key, "bull:{myqueue}:"))
		})
	}
}

func TestKeyBuilder_JobKeys(t *testing.T) {
	// Test with hash tags enabled (cluster mode)
	kb := bullmq.NewKeyBuilderWithHashTags("testqueue", true)

	t.Run("Job", func(t *testing.T) {
		key := kb.Job("job-123")
		assert.Equal(t, "bull:{testqueue}:job-123", key)
		assert.Contains(t, key, "{testqueue}")
	})

	t.Run("Lock", func(t *testing.T) {
		key := kb.Lock("job-456")
		assert.Equal(t, "bull:{testqueue}:job-456:lock", key)
		assert.Contains(t, key, "{testqueue}")
	})

	t.Run("Logs", func(t *testing.T) {
		key := kb.Logs("job-789")
		assert.Equal(t, "bull:{testqueue}:job-789:logs", key)
		assert.Contains(t, key, "{testqueue}")
	})
}

func TestKeyBuilder_AllKeysUseHashTags(t *testing.T) {
	// Test with hash tags enabled (cluster mode)
	kb := bullmq.NewKeyBuilderWithHashTags("production-queue", true)

	// Get all keys
	keys := []string{
		kb.Wait(),
		kb.Prioritized(),
		kb.Delayed(),
		kb.Active(),
		kb.Completed(),
		kb.Failed(),
		kb.Events(),
		kb.Meta(),
		kb.Job("test-job"),
		kb.Lock("test-job"),
		kb.Logs("test-job"),
	}

	// Verify all keys contain hash tag
	for _, key := range keys {
		assert.Contains(t, key, "{production-queue}",
			"Key %s must contain hash tag {production-queue} for cluster compatibility", key)
	}
}
