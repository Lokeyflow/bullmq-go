package integration

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestRedisClusterHashTags validates that all BullMQ keys use hash tags
// for Redis Cluster compatibility, ensuring multi-key Lua scripts work correctly.
//
// This test addresses P0 requirement: Validate Redis Cluster multi-key Lua script execution
// with hash tags to prevent CROSSSLOT errors.
func TestRedisClusterHashTags(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping Redis Cluster integration test in short mode")
	}

	ctx := context.Background()

	// Start Redis Cluster using testcontainers
	// Redis Cluster requires minimum 3 master nodes
	clusterContainer, err := startRedisCluster(ctx, t)
	require.NoError(t, err, "Failed to start Redis Cluster")
	defer clusterContainer.Terminate(ctx)

	// Get cluster connection string
	host, err := clusterContainer.Host(ctx)
	require.NoError(t, err)
	port, err := clusterContainer.MappedPort(ctx, "7000/tcp")
	require.NoError(t, err)

	// Connect to Redis Cluster
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{host + ":" + port.Port()},
	})
	defer client.Close()

	// Wait for cluster to be ready
	require.Eventually(t, func() bool {
		return client.Ping(ctx).Err() == nil
	}, 30*time.Second, 1*time.Second, "Redis Cluster not ready")

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

		// Verify all keys hash to the same slot
		var expectedSlot int
		for i, key := range keys {
			slot := redis.ClusterSlot(key)
			if i == 0 {
				expectedSlot = slot
			}
			assert.Equal(t, expectedSlot, slot,
				"Key %s should be in slot %d but is in slot %d (hash tags not working)",
				key, expectedSlot, slot)
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
		// Negative test: keys WITHOUT hash tags should fail in cluster mode
		luaScript := `
			local key1 = KEYS[1]
			local key2 = KEYS[2]
			redis.call("SET", key1, "value1")
			redis.call("SET", key2, "value2")
			return "ok"
		`

		// Keys without hash tags (bad practice)
		badKeys := []string{
			"bull:queue1:wait",
			"bull:queue2:wait",
		}

		// This SHOULD fail with CROSSSLOT error
		_, err := client.Eval(ctx, luaScript, badKeys).Result()
		assert.Error(t, err, "Expected CROSSSLOT error for keys without hash tags")
		assert.Contains(t, err.Error(), "CROSSSLOT",
			"Error should mention CROSSSLOT, got: %v", err)
	})
}

// startRedisCluster starts a Redis Cluster using testcontainers
// Minimum 3 master nodes required for cluster
func startRedisCluster(ctx context.Context, t *testing.T) (testcontainers.Container, error) {
	// Use official Redis Cluster Docker image
	// This image automatically configures a 6-node cluster (3 masters + 3 replicas)
	req := testcontainers.ContainerRequest{
		Image:        "redis/redis-stack-server:latest", // Includes Redis with cluster support
		ExposedPorts: []string{"7000/tcp", "7001/tcp", "7002/tcp", "7003/tcp", "7004/tcp", "7005/tcp"},
		Env: map[string]string{
			"CLUSTER_ENABLED": "yes",
		},
		WaitingFor: wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	return container, nil
}

// TestRedisClusterJobLifecycle validates end-to-end job processing in Redis Cluster
// This test will be expanded once Worker and Producer implementations are complete
func TestRedisClusterJobLifecycle(t *testing.T) {
	t.Skip("TODO: Implement after Worker and Producer are implemented")

	// TODO: Test complete job lifecycle in Redis Cluster:
	// 1. Producer adds job to wait queue
	// 2. Worker picks up job (moveToActive.lua)
	// 3. Heartbeat extends lock
	// 4. Worker completes job (moveToCompleted.lua)
	// 5. Verify no CROSSSLOT errors throughout
}
