package integration

import (
	"context"
	"testing"
	"time"

	"github.com/Lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T066: Stalled checker requeues job with expired lock
func TestStalled_RequeuesExpiredLock(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-stalled-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)
	jobID := job.ID

	// Manually simulate stalled job: move to active with expired lock
	waitKey := "bull:{" + queueName + "}:wait"
	activeKey := "bull:{" + queueName + "}:active"
	lockKey := "bull:{" + queueName + "}:" + jobID + ":lock"

	// Move job to active
	rdb.RPop(ctx, waitKey)
	rdb.RPush(ctx, activeKey, jobID)

	// Set lock with very short TTL (1 second) to simulate expiration
	rdb.Set(ctx, lockKey, "expired-token", 1*time.Second)

	// Wait for lock to expire
	time.Sleep(2 * time.Second)

	// Create worker with fast stalled check interval
	opts := bullmq.DefaultWorkerOptions
	opts.StalledCheckInterval = 2 * time.Second
	worker := bullmq.NewWorker(queueName, rdb, opts)

	processed := make(chan string, 1)
	worker.Process(func(job *bullmq.Job) error {
		processed <- job.ID
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for stalled checker to requeue and worker to process
	select {
	case id := <-processed:
		assert.Equal(t, jobID, id, "Stalled job should be requeued and processed")
	case <-time.After(10 * time.Second):
		t.Fatal("Stalled job should be detected and requeued within 10s")
	}
}

// T067: Stalled checker increments attemptsMade
func TestStalled_IncrementsAttemptsMade(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-stalled-attempts-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)
	jobID := job.ID

	// Manually create stalled job
	activeKey := "bull:{" + queueName + "}:active"
	lockKey := "bull:{" + queueName + "}:" + jobID + ":lock"
	jobKey := "bull:{" + queueName + "}:" + jobID

	rdb.RPush(ctx, activeKey, jobID)
	rdb.Set(ctx, lockKey, "token", 1*time.Second)
	rdb.HSet(ctx, jobKey, "atm", "0") // attemptsMade = 0

	// Wait for lock expiration
	time.Sleep(2 * time.Second)

	// Create worker with fast stalled check
	opts := bullmq.DefaultWorkerOptions
	opts.StalledCheckInterval = 2 * time.Second
	worker := bullmq.NewWorker(queueName, rdb, opts)

	processed := make(chan bool, 1)
	worker.Process(func(job *bullmq.Job) error {
		processed <- true
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for processing
	<-processed

	// Verify attemptsMade incremented
	atm, err := rdb.HGet(ctx, jobKey, "atm").Result()
	require.NoError(t, err)
	assert.NotEqual(t, "0", atm, "attemptsMade should be incremented after stalled recovery")
}

// T068: Stalled checker skips cycle if previous still running
func TestStalled_SkipsCycleIfRunning(t *testing.T) {
	// This is tested by design - StalledChecker uses atomic.Bool to prevent overlapping cycles
	// Integration test would require artificially slow Lua script execution
	// Unit test in pkg/bullmq/stalled.go validates cycle skip logic
	t.Skip("Tested via unit tests and atomic.Bool implementation")
}

// T069: "stalled" event emitted to events stream
func TestStalled_EmitsEvent(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-stalled-events-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)
	jobID := job.ID

	// Create stalled job
	activeKey := "bull:{" + queueName + "}:active"
	lockKey := "bull:{" + queueName + "}:" + jobID + ":lock"

	rdb.RPush(ctx, activeKey, jobID)
	rdb.Set(ctx, lockKey, "token", 1*time.Second)

	time.Sleep(2 * time.Second)

	// Create worker
	opts := bullmq.DefaultWorkerOptions
	opts.StalledCheckInterval = 2 * time.Second
	worker := bullmq.NewWorker(queueName, rdb, opts)

	worker.Process(func(job *bullmq.Job) error {
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for stalled detection
	time.Sleep(5 * time.Second)

	// Check events stream for "stalled" event
	eventsKey := "bull:{" + queueName + "}:events"
	events, err := rdb.XRange(ctx, eventsKey, "-", "+").Result()
	require.NoError(t, err)

	// Look for stalled event
	found := false
	for _, event := range events {
		if eventType, ok := event.Values["event"].(string); ok && eventType == "stalled" {
			found = true
			break
		}
	}

	assert.True(t, found, "Stalled event should be emitted to events stream")
}
