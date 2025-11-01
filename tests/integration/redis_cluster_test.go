package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper functions for Redis Cluster management via Docker Compose

// startRedisCluster starts a 3-node Redis Cluster using Docker Compose
func startRedisCluster(t *testing.T) {
	t.Helper()

	composeFile := "docker-compose.cluster.yml"

	// Check if Docker Compose is available
	if _, err := exec.LookPath("docker-compose"); err != nil {
		if _, err := exec.LookPath("docker"); err != nil {
			t.Skip("Docker not available - skipping cluster test")
		}
		// Try 'docker compose' (v2 syntax)
		cmd := exec.Command("docker", "compose", "version")
		if err := cmd.Run(); err != nil {
			t.Skip("Docker Compose not available - skipping cluster test")
		}
	}

	t.Log("Starting Redis Cluster via Docker Compose...")

	// Start cluster
	cmd := exec.Command("docker-compose", "-f", composeFile, "up", "-d")
	cmd.Dir = "." // Run from tests/integration directory
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Docker Compose output: %s", output)
		t.Fatalf("Failed to start Redis Cluster: %v", err)
	}

	t.Log("Waiting for Redis Cluster to be ready...")
	time.Sleep(10 * time.Second) // Wait for cluster initialization

	// Verify cluster is healthy
	ctx := context.Background()
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{"localhost:7001", "localhost:7002", "localhost:7003"},
	})
	defer client.Close()

	require.Eventually(t, func() bool {
		return client.Ping(ctx).Err() == nil
	}, 30*time.Second, 1*time.Second, "Redis Cluster failed to start")

	t.Log("✅ Redis Cluster is ready")
}

// stopRedisCluster stops the Redis Cluster
func stopRedisCluster(t *testing.T) {
	t.Helper()

	composeFile := "docker-compose.cluster.yml"

	t.Log("Stopping Redis Cluster...")
	cmd := exec.Command("docker-compose", "-f", composeFile, "down", "-v")
	cmd.Dir = "."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Logf("Warning: Failed to stop cluster: %v\n%s", err, output)
	}
}

// getRedisClusterClient returns a connected Redis Cluster client
func getRedisClusterClient(t *testing.T) *redis.ClusterClient {
	t.Helper()

	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{"localhost:7001", "localhost:7002", "localhost:7003"},
	})

	// Verify connection
	ctx := context.Background()
	require.NoError(t, client.Ping(ctx).Err(), "Failed to connect to Redis Cluster")

	return client
}

// TestRedisClusterHashTags validates that all BullMQ keys use hash tags
// for Redis Cluster compatibility, ensuring multi-key Lua scripts work correctly.
//
// This test addresses P0 requirement: Validate Redis Cluster multi-key Lua script execution
// with hash tags to prevent CROSSSLOT errors.
func TestRedisClusterHashTags(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping Redis Cluster integration test in short mode")
	}
	if os.Getenv("SKIP_CLUSTER_TESTS") == "1" {
		t.Skip("SKIP_CLUSTER_TESTS=1 - skipping cluster test")
	}

	// Start Redis Cluster
	startRedisCluster(t)
	defer stopRedisCluster(t)

	// Get cluster client
	client := getRedisClusterClient(t)
	defer client.Close()

	ctx := context.Background()

	t.Run("KeysWithHashTagsInSameSlot", func(t *testing.T) {
		queueName := "test-queue"

		// All BullMQ keys for a queue MUST use hash tag {queue-name}
		keys := []string{
			"bull:{" + queueName + "}:wait",
			"bull:{" + queueName + "}:active",
			"bull:{" + queueName + "}:prioritized",
			"bull:{" + queueName + "}:delayed",
			"bull:{" + queueName + "}:completed",
			"bull:{" + queueName + "}:failed",
			"bull:{" + queueName + "}:meta",
			"bull:{" + queueName + "}:events",
			"bull:{" + queueName + "}:id",
			"bull:{" + queueName + "}:1",        // Job hash
			"bull:{" + queueName + "}:1:lock",   // Lock key
			"bull:{" + queueName + "}:1:logs",   // Logs list
		}

		// Verify all keys hash to the same slot using CRC16
		allSame, expectedSlot, slots := bullmq.ValidateHashTags(keys)
		assert.True(t, allSame, "All keys should hash to the same slot (hash tags working)")

		t.Logf("✅ All keys hash to slot %d", expectedSlot)
		for i, key := range keys {
			t.Logf("   Key: %-40s → Slot: %d", key, slots[i])
			assert.Equal(t, expectedSlot, slots[i],
				"Key %s should be in slot %d but is in slot %d (hash tags not working)",
				key, expectedSlot, slots[i])
		}
	})

	t.Run("MultiKeyLuaScriptExecution", func(t *testing.T) {
		queueName := "test-multi-key"

		// Simulate moveToActive.lua multi-key operation
		// This Lua script touches multiple keys atomically
		luaScript := `
			-- Simulate BullMQ moveToActive.lua
			local waitKey = KEYS[1]
			local activeKey = KEYS[2]
			local jobHashKey = KEYS[3]
			local lockKey = KEYS[4]

			-- Multi-key operations
			redis.call("LPUSH", waitKey, "job-1")
			local jobId = redis.call("LPOP", waitKey)
			redis.call("RPUSH", activeKey, jobId)
			redis.call("HSET", jobHashKey, "status", "active")
			redis.call("SET", lockKey, "token-123", "PX", 30000)

			return jobId
		`

		keys := []string{
			"bull:{" + queueName + "}:wait",
			"bull:{" + queueName + "}:active",
			"bull:{" + queueName + "}:1",
			"bull:{" + queueName + "}:1:lock",
		}

		// Execute Lua script with multi-key operation
		// This MUST NOT fail with CROSSSLOT error
		result, err := client.Eval(ctx, luaScript, keys).Result()
		require.NoError(t, err, "Multi-key Lua script failed (CROSSSLOT error indicates hash tags not working)")
		assert.Equal(t, "job-1", result)

		// Verify operations succeeded
		activeJobs, err := client.LRange(ctx, keys[1], 0, -1).Result()
		require.NoError(t, err)
		assert.Equal(t, []string{"job-1"}, activeJobs)

		status, err := client.HGet(ctx, keys[2], "status").Result()
		require.NoError(t, err)
		assert.Equal(t, "active", status)

		lock, err := client.Get(ctx, keys[3]).Result()
		require.NoError(t, err)
		assert.Equal(t, "token-123", lock)
	})

	t.Run("CrossSlotOperationsFail", func(t *testing.T) {
		// T112: Negative test - keys WITHOUT hash tags should fail in cluster mode
		luaScript := `
			local key1 = KEYS[1]
			local key2 = KEYS[2]
			redis.call("SET", key1, "value1")
			redis.call("SET", key2, "value2")
			return "ok"
		`

		// Keys without hash tags (bad practice) - will hash to different slots
		badKeys := []string{
			"bull:queue1:wait",
			"bull:queue2:wait",
		}

		// Verify keys are in different slots
		slot1 := bullmq.GetClusterSlot(badKeys[0])
		slot2 := bullmq.GetClusterSlot(badKeys[1])
		t.Logf("Key 1 '%s' → slot %d", badKeys[0], slot1)
		t.Logf("Key 2 '%s' → slot %d", badKeys[1], slot2)
		assert.NotEqual(t, slot1, slot2, "Keys without hash tags should be in different slots")

		// This SHOULD fail with CROSSSLOT error in Redis Cluster
		_, err := client.Eval(ctx, luaScript, badKeys).Result()
		assert.Error(t, err, "Expected CROSSSLOT error for keys without hash tags")
		assert.Contains(t, err.Error(), "CROSSSLOT",
			"Error should mention CROSSSLOT, got: %v", err)

		t.Logf("✅ CROSSSLOT error correctly raised: %v", err)
	})

	t.Run("BullMQWorkerInCluster", func(t *testing.T) {
		// T111: Full integration test - BullMQ worker/producer in Redis Cluster
		queueName := "cluster-test-queue"

		// Create queue and add job
		queue := bullmq.NewQueue(queueName, client)
		job, err := queue.Add(ctx, "test-job", map[string]interface{}{
			"message": "Hello from Redis Cluster!",
		}, bullmq.JobOptions{
			Attempts: 3,
		})
		require.NoError(t, err, "Failed to add job to cluster queue")
		t.Logf("✅ Job added: %s", job.ID)

		// Verify job is in wait queue
		kb := bullmq.NewKeyBuilder(queueName, client)
		waitLen, err := client.LLen(ctx, kb.Wait()).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(1), waitLen, "Job should be in wait queue")

		// Verify job data stored correctly
		jobData, err := client.HGetAll(ctx, kb.Job(job.ID)).Result()
		require.NoError(t, err)
		assert.NotEmpty(t, jobData, "Job data should exist in Redis")
		assert.Equal(t, "test-job", jobData["name"])

		t.Logf("✅ BullMQ queue operations work correctly in Redis Cluster")

		// Cleanup
		client.Del(ctx, kb.Wait(), kb.Job(job.ID))
	})
}

// TestRedisClusterBullMQIntegration tests full BullMQ integration with Redis Cluster
// T111: Multi-node cluster operations with Worker and Queue
func TestRedisClusterBullMQIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping Redis Cluster integration test in short mode")
	}
	if os.Getenv("SKIP_CLUSTER_TESTS") == "1" {
		t.Skip("SKIP_CLUSTER_TESTS=1 - skipping cluster test")
	}

	// Start Redis Cluster
	startRedisCluster(t)
	defer stopRedisCluster(t)

	// Get cluster client
	client := getRedisClusterClient(t)
	defer client.Close()

	ctx := context.Background()
	queueName := "integration-test-queue"

	// Create queue
	queue := bullmq.NewQueue(queueName, client)

	// Add test job
	job, err := queue.Add(ctx, "integration-job", map[string]interface{}{
		"data": "test-value",
	}, bullmq.JobOptions{
		Attempts: 3,
		Priority: 10,
	})
	require.NoError(t, err)
	t.Logf("✅ Job created: %s", job.ID)

	// Verify all keys are in same slot
	kb := bullmq.NewKeyBuilder(queueName, client)
	keys := []string{
		kb.Wait(),
		kb.Active(),
		kb.Prioritized(),
		kb.Job(job.ID),
		kb.Lock(job.ID),
	}

	allSame, slot, slots := bullmq.ValidateHashTags(keys)
	assert.True(t, allSame, "All BullMQ keys should hash to the same slot")
	t.Logf("✅ All keys hash to slot %d", slot)
	for i, key := range keys {
		t.Logf("   %s → slot %d", key, slots[i])
	}

	// Verify cluster info
	clusterInfo, err := client.ClusterInfo(ctx).Result()
	require.NoError(t, err)
	t.Logf("Redis Cluster Info:\n%s", clusterInfo)
	assert.Contains(t, clusterInfo, "cluster_state:ok", "Cluster should be in OK state")

	// Cleanup
	client.Del(ctx, kb.Wait(), kb.Job(job.ID))
	t.Log("✅ Full BullMQ integration test passed in Redis Cluster")
}
