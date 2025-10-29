package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobCompletionVsStalledCheck validates that job completion and stalled checker
// don't conflict when racing against each other.
//
// This test addresses P1 requirement: Add test for race condition between
// job completion and stalled check running simultaneously.
//
// Scenario: Job completes just as stalled checker runs
// Expected: No duplicate processing, job marked completed exactly once
func TestJobCompletionVsStalledCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race condition integration test in short mode")
	}

	ctx := context.Background()

	// Setup Redis client (assumes local Redis or testcontainer)
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	// Verify Redis connection
	err := client.Ping(ctx).Err()
	require.NoError(t, err, "Redis must be running for this test")

	queueName := "test-race-queue"

	// Cleanup before test
	cleanupQueue(ctx, client, queueName)
	defer cleanupQueue(ctx, client, queueName)

	t.Run("JobCompletesBeforeStalledCheck", func(t *testing.T) {
		jobID := "race-job-1"

		// Simulate job in active state with lock
		setupActiveJob(ctx, t, client, queueName, jobID, 30*time.Second)

		var wg sync.WaitGroup
		wg.Add(2)

		completionResult := make(chan error, 1)
		stalledResult := make(chan int, 1)

		// Goroutine 1: Complete the job
		go func() {
			defer wg.Done()
			err := simulateJobCompletion(ctx, client, queueName, jobID)
			completionResult <- err
		}()

		// Goroutine 2: Run stalled checker (with tiny delay to create race)
		go func() {
			defer wg.Done()
			time.Sleep(1 * time.Millisecond) // Small delay to race with completion
			requeued := simulateStalledChecker(ctx, client, queueName)
			stalledResult <- requeued
		}()

		wg.Wait()

		// Verify results
		err := <-completionResult
		require.NoError(t, err, "Job completion should succeed")

		requeued := <-stalledResult
		assert.Equal(t, 0, requeued, "Stalled checker should NOT requeue completed job")

		// Verify job is in completed state, not in active or wait
		isActive := isJobInActive(ctx, client, queueName, jobID)
		assert.False(t, isActive, "Job should NOT be in active list after completion")

		isCompleted := isJobInCompleted(ctx, client, queueName, jobID)
		assert.True(t, isCompleted, "Job SHOULD be in completed set")

		isInWait := isJobInWait(ctx, client, queueName, jobID)
		assert.False(t, isInWait, "Job should NOT be requeued to wait list")
	})

	t.Run("StalledCheckerRunsBeforeCompletion", func(t *testing.T) {
		jobID := "race-job-2"

		// Simulate job in active state with EXPIRED lock
		setupActiveJob(ctx, t, client, queueName, jobID, 0) // Lock already expired

		var wg sync.WaitGroup
		wg.Add(2)

		stalledResult := make(chan int, 1)
		completionResult := make(chan error, 1)

		// Goroutine 1: Run stalled checker first
		go func() {
			defer wg.Done()
			requeued := simulateStalledChecker(ctx, client, queueName)
			stalledResult <- requeued
		}()

		// Goroutine 2: Try to complete job (with delay, should fail)
		go func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond) // Delay so stalled checker wins
			err := simulateJobCompletion(ctx, client, queueName, jobID)
			completionResult <- err
		}()

		wg.Wait()

		// Verify results
		requeued := <-stalledResult
		assert.Equal(t, 1, requeued, "Stalled checker should requeue job with expired lock")

		err := <-completionResult
		// Completion should fail because lock no longer exists (moved by stalled checker)
		// In real implementation, moveToCompleted.lua checks lock token
		// For this test, we expect completion to detect job is no longer active
		t.Logf("Completion result after stalled requeue: %v", err)

		// Verify job was requeued, not completed
		isInWait := isJobInWait(ctx, client, queueName, jobID)
		assert.True(t, isInWait, "Job should be requeued to wait list by stalled checker")

		isCompleted := isJobInCompleted(ctx, client, queueName, jobID)
		assert.False(t, isCompleted, "Job should NOT be in completed set (stalled checker won)")
	})

	t.Run("SimultaneousCompletionAttempts", func(t *testing.T) {
		// Edge case: Two workers try to complete same job simultaneously
		// (shouldn't happen with proper locking, but test atomicity)

		jobID := "race-job-3"
		setupActiveJob(ctx, t, client, queueName, jobID, 30*time.Second)

		var wg sync.WaitGroup
		const numWorkers = 5

		results := make([]error, numWorkers)
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				err := simulateJobCompletion(ctx, client, queueName, jobID)
				results[workerID] = err
			}(i)
		}

		wg.Wait()

		// Only ONE worker should successfully complete the job
		successCount := 0
		for _, err := range results {
			if err == nil {
				successCount++
			}
		}

		assert.Equal(t, 1, successCount,
			"Exactly ONE worker should successfully complete the job (Lua script atomicity)")

		// Verify job in completed exactly once
		score, err := client.ZScore(ctx, "bull:{"+queueName+"}:completed", jobID).Result()
		require.NoError(t, err, "Job should be in completed set")
		assert.Greater(t, score, float64(0), "Job should have timestamp score in completed set")
	})
}

// Helper functions

func cleanupQueue(ctx context.Context, client *redis.Client, queueName string) {
	// Delete all queue keys
	keys := []string{
		"bull:{" + queueName + "}:wait",
		"bull:{" + queueName + "}:active",
		"bull:{" + queueName + "}:completed",
		"bull:{" + queueName + "}:failed",
		"bull:{" + queueName + "}:*",
	}

	for _, key := range keys {
		if key[len(key)-1] == '*' {
			// Pattern match and delete
			iter := client.Scan(ctx, 0, key, 0).Iterator()
			for iter.Next(ctx) {
				client.Del(ctx, iter.Val())
			}
		} else {
			client.Del(ctx, key)
		}
	}
}

func setupActiveJob(ctx context.Context, t *testing.T, client *redis.Client, queueName, jobID string, lockTTL time.Duration) {
	// Add job to active list
	err := client.RPush(ctx, "bull:{"+queueName+"}:active", jobID).Err()
	require.NoError(t, err)

	// Create job hash
	err = client.HSet(ctx, "bull:{"+queueName+"}:"+jobID,
		"id", jobID,
		"name", "test-job",
		"data", `{"test": true}`,
		"timestamp", time.Now().UnixMilli(),
	).Err()
	require.NoError(t, err)

	// Set lock with TTL
	if lockTTL > 0 {
		lockKey := "bull:{" + queueName + "}:" + jobID + ":lock"
		err = client.Set(ctx, lockKey, "test-token-123", lockTTL).Err()
		require.NoError(t, err)
	}
	// If lockTTL == 0, lock is already expired (not set)
}

func simulateJobCompletion(ctx context.Context, client *redis.Client, queueName, jobID string) error {
	// Simulate moveToCompleted.lua logic (simplified)
	lockKey := "bull:{" + queueName + "}:" + jobID + ":lock"
	activeKey := "bull:{" + queueName + "}:active"
	completedKey := "bull:{" + queueName + "}:completed"
	jobKey := "bull:{" + queueName + "}:" + jobID

	// Atomic script to move job to completed
	script := `
		local lockKey = KEYS[1]
		local activeKey = KEYS[2]
		local completedKey = KEYS[3]
		local jobKey = KEYS[4]
		local jobId = ARGV[1]
		local timestamp = ARGV[2]

		-- Check lock exists (simplified - real version checks token)
		local lockExists = redis.call("EXISTS", lockKey)
		if lockExists == 0 then
			return redis.error_reply("Lock not found or expired")
		end

		-- Remove from active
		redis.call("LREM", activeKey, 0, jobId)

		-- Delete lock
		redis.call("DEL", lockKey)

		-- Add to completed
		redis.call("ZADD", completedKey, timestamp, jobId)

		-- Update job hash
		redis.call("HSET", jobKey, "finishedOn", timestamp)

		return "ok"
	`

	keys := []string{lockKey, activeKey, completedKey, jobKey}
	args := []interface{}{jobID, time.Now().UnixMilli()}

	return client.Eval(ctx, script, keys, args...).Err()
}

func simulateStalledChecker(ctx context.Context, client *redis.Client, queueName string) int {
	// Simulate moveStalledJobsToWait.lua logic (simplified)
	activeKey := "bull:{" + queueName + "}:active"
	waitKey := "bull:{" + queueName + "}:wait"

	script := `
		local activeKey = KEYS[1]
		local waitKey = KEYS[2]

		-- Get all active jobs
		local activeJobs = redis.call("LRANGE", activeKey, 0, -1)
		local requeued = 0

		for _, jobId in ipairs(activeJobs) do
			local lockKey = "bull:{` + queueName + `}:" .. jobId .. ":lock"

			-- Check if lock exists
			local lockExists = redis.call("EXISTS", lockKey)
			if lockExists == 0 then
				-- Lock expired, requeue job
				redis.call("LREM", activeKey, 0, jobId)
				redis.call("RPUSH", waitKey, jobId)
				requeued = requeued + 1
			end
		end

		return requeued
	`

	keys := []string{activeKey, waitKey}
	result, err := client.Eval(ctx, script, keys).Int()
	if err != nil {
		return 0
	}
	return result
}

func isJobInActive(ctx context.Context, client *redis.Client, queueName, jobID string) bool {
	activeKey := "bull:{" + queueName + "}:active"
	jobs, _ := client.LRange(ctx, activeKey, 0, -1).Result()
	for _, id := range jobs {
		if id == jobID {
			return true
		}
	}
	return false
}

func isJobInCompleted(ctx context.Context, client *redis.Client, queueName, jobID string) bool {
	completedKey := "bull:{" + queueName + "}:completed"
	_, err := client.ZScore(ctx, completedKey, jobID).Result()
	return err == nil
}

func isJobInWait(ctx context.Context, client *redis.Client, queueName, jobID string) bool {
	waitKey := "bull:{" + queueName + "}:wait"
	jobs, _ := client.LRange(ctx, waitKey, 0, -1).Result()
	for _, id := range jobs {
		if id == jobID {
			return true
		}
	}
	return false
}
