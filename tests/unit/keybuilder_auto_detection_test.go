package unit

import (
	"testing"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// TestKeyBuilder_AutoDetection_SingleInstance verifies that KeyBuilder
// does NOT use hash tags when given a single-instance Redis client
func TestKeyBuilder_AutoDetection_SingleInstance(t *testing.T) {
	queueName := "test-queue"

	// Create single-instance Redis client
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	// Create KeyBuilder with auto-detection
	kb := bullmq.NewKeyBuilder(queueName, client)

	// Verify keys do NOT have hash tags (single-instance mode)
	tests := []struct {
		name     string
		keyFunc  func() string
		expected string
	}{
		{"Wait", kb.Wait, "bull:test-queue:wait"},
		{"Prioritized", kb.Prioritized, "bull:test-queue:prioritized"},
		{"Delayed", kb.Delayed, "bull:test-queue:delayed"},
		{"Active", kb.Active, "bull:test-queue:active"},
		{"Completed", kb.Completed, "bull:test-queue:completed"},
		{"Failed", kb.Failed, "bull:test-queue:failed"},
		{"Events", kb.Events, "bull:test-queue:events"},
		{"Meta", kb.Meta, "bull:test-queue:meta"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := tt.keyFunc()
			assert.Equal(t, tt.expected, key,
				"Single-instance Redis should NOT use hash tags")
			assert.NotContains(t, key, "{",
				"Key should not contain curly braces")
			assert.NotContains(t, key, "}",
				"Key should not contain curly braces")
		})
	}
}

// TestKeyBuilder_AutoDetection_Cluster verifies that KeyBuilder
// DOES use hash tags when given a Redis Cluster client
func TestKeyBuilder_AutoDetection_Cluster(t *testing.T) {
	queueName := "test-queue"

	// Create Redis Cluster client (even if cluster is not running,
	// we're just testing the type detection logic)
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{"localhost:7001"},
	})
	defer client.Close()

	// Create KeyBuilder with auto-detection
	kb := bullmq.NewKeyBuilder(queueName, client)

	// Verify keys HAVE hash tags (cluster mode)
	tests := []struct {
		name     string
		keyFunc  func() string
		expected string
	}{
		{"Wait", kb.Wait, "bull:{test-queue}:wait"},
		{"Prioritized", kb.Prioritized, "bull:{test-queue}:prioritized"},
		{"Delayed", kb.Delayed, "bull:{test-queue}:delayed"},
		{"Active", kb.Active, "bull:{test-queue}:active"},
		{"Completed", kb.Completed, "bull:{test-queue}:completed"},
		{"Failed", kb.Failed, "bull:{test-queue}:failed"},
		{"Events", kb.Events, "bull:{test-queue}:events"},
		{"Meta", kb.Meta, "bull:{test-queue}:meta"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := tt.keyFunc()
			assert.Equal(t, tt.expected, key,
				"Cluster Redis SHOULD use hash tags")
			assert.Contains(t, key, "{test-queue}",
				"Key must contain hash tag {test-queue}")
		})
	}
}

// TestKeyBuilder_ExplicitHashTags_Override verifies that
// NewKeyBuilderWithHashTags allows explicit control
func TestKeyBuilder_ExplicitHashTags_Override(t *testing.T) {
	queueName := "explicit-queue"

	t.Run("ForceHashTagsOn", func(t *testing.T) {
		// Force hash tags ON (regardless of client type)
		kb := bullmq.NewKeyBuilderWithHashTags(queueName, true)

		assert.Equal(t, "bull:{explicit-queue}:wait", kb.Wait())
		assert.Equal(t, "bull:{explicit-queue}:active", kb.Active())
	})

	t.Run("ForceHashTagsOff", func(t *testing.T) {
		// Force hash tags OFF (regardless of client type)
		kb := bullmq.NewKeyBuilderWithHashTags(queueName, false)

		assert.Equal(t, "bull:explicit-queue:wait", kb.Wait())
		assert.Equal(t, "bull:explicit-queue:active", kb.Active())
	})
}

// TestKeyBuilder_JobKeys_AutoDetection verifies job-specific keys
// also respect auto-detection
func TestKeyBuilder_JobKeys_AutoDetection(t *testing.T) {
	queueName := "job-test-queue"
	jobID := "test-job-123"

	t.Run("SingleInstance", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
		defer client.Close()

		kb := bullmq.NewKeyBuilder(queueName, client)

		assert.Equal(t, "bull:job-test-queue:test-job-123", kb.Job(jobID))
		assert.Equal(t, "bull:job-test-queue:test-job-123:lock", kb.Lock(jobID))
		assert.Equal(t, "bull:job-test-queue:test-job-123:logs", kb.Logs(jobID))
	})

	t.Run("Cluster", func(t *testing.T) {
		client := redis.NewClusterClient(&redis.ClusterOptions{
			Addrs: []string{"localhost:7001"},
		})
		defer client.Close()

		kb := bullmq.NewKeyBuilder(queueName, client)

		assert.Equal(t, "bull:{job-test-queue}:test-job-123", kb.Job(jobID))
		assert.Equal(t, "bull:{job-test-queue}:test-job-123:lock", kb.Lock(jobID))
		assert.Equal(t, "bull:{job-test-queue}:test-job-123:logs", kb.Logs(jobID))
	})
}

// TestIsRedisCluster verifies the cluster detection function
func TestIsRedisCluster(t *testing.T) {
	t.Run("SingleInstanceClient", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
		defer client.Close()

		isCluster := bullmq.IsRedisCluster(client)
		assert.False(t, isCluster, "Single-instance client should return false")
	})

	t.Run("ClusterClient", func(t *testing.T) {
		client := redis.NewClusterClient(&redis.ClusterOptions{
			Addrs: []string{"localhost:7001"},
		})
		defer client.Close()

		isCluster := bullmq.IsRedisCluster(client)
		assert.True(t, isCluster, "Cluster client should return true")
	})

	t.Run("NilClient", func(t *testing.T) {
		isCluster := bullmq.IsRedisCluster(nil)
		assert.False(t, isCluster, "Nil client should return false (safe default)")
	})
}
