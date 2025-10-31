package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T039: Worker picks up job from wait queue
func TestWorker_PickupFromWaitQueue(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-wait-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	// Add job to queue
	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{
		Attempts: 3,
		Backoff:  bullmq.BackoffConfig{Type: "exponential", Delay: 1000},
	})
	require.NoError(t, err)

	// Verify job in wait queue
	waitLen, _ := rdb.LLen(ctx, "bull:{"+queueName+"}:wait").Result()
	assert.Equal(t, int64(1), waitLen)

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processed := make(chan string, 1)

	worker.Process(func(job *bullmq.Job) error {
		processed <- job.ID
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for job processing
	select {
	case id := <-processed:
		assert.Equal(t, job.ID, id)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for job processing")
	}

	// Verify job moved from wait to active (then completed)
	waitLen, _ = rdb.LLen(ctx, "bull:{"+queueName+"}:wait").Result()
	assert.Equal(t, int64(0), waitLen)
}

// T040: Worker picks up job from prioritized queue (priority order)
func TestWorker_PickupPriorityOrder(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-priority-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	// Add jobs with different priorities
	_, err := queue.Add(ctx, "low-priority", map[string]interface{}{"priority": 1}, bullmq.JobOptions{Priority: 1})
	require.NoError(t, err)

	highPriorityJob, err := queue.Add(ctx, "high-priority", map[string]interface{}{"priority": 10}, bullmq.JobOptions{Priority: 10})
	require.NoError(t, err)

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processedIDs := make(chan string, 2)

	worker.Process(func(job *bullmq.Job) error {
		processedIDs <- job.ID
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// First job should be high priority
	select {
	case id := <-processedIDs:
		assert.Equal(t, highPriorityJob.ID, id, "High priority job should be processed first")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for first job")
	}
}

// T041: Worker respects paused queue state
func TestWorker_RespectsPausedQueue(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-paused-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	// Pause queue
	require.NoError(t, queue.Pause(ctx))

	// Add job
	_, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processed := make(chan string, 1)

	worker.Process(func(job *bullmq.Job) error {
		processed <- job.ID
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Job should NOT be processed (queue paused)
	select {
	case <-processed:
		t.Fatal("Job should not be processed when queue is paused")
	case <-time.After(1 * time.Second):
		// Expected - no job processed
	}

	// Resume and verify job is now processed
	require.NoError(t, queue.Resume(ctx))

	select {
	case <-processed:
		// Expected - job processed after resume
	case <-time.After(2 * time.Second):
		t.Fatal("Job should be processed after queue resume")
	}
}

// T042: Lock acquired with UUID v4 token on pickup
func TestWorker_LockAcquiredWithUUIDv4(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-lock-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Create worker with long job processing
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	started := make(chan string, 1)

	worker.Process(func(job *bullmq.Job) error {
		started <- job.ID
		time.Sleep(500 * time.Millisecond) // Keep job active
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for job to be picked up
	select {
	case <-started:
		// Check lock exists
		lockKey := "bull:{" + queueName + "}:" + job.ID + ":lock"
		lockToken, err := rdb.Get(ctx, lockKey).Result()
		require.NoError(t, err)

		// Validate UUID v4 format (36 chars with dashes)
		assert.Len(t, lockToken, 36, "Lock token should be UUID v4 format")
		assert.Contains(t, lockToken, "-", "Lock token should contain dashes (UUID format)")

	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for job pickup")
	}
}

// T043: Job moves wait→active atomically
func TestWorker_AtomicWaitToActive(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-atomic-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)
	jobID := job.ID

	// Verify job in wait queue
	waitKey := "bull:{" + queueName + "}:wait"
	waitLen, _ := rdb.LLen(ctx, waitKey).Result()
	assert.Equal(t, int64(1), waitLen)

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	started := make(chan string, 1)

	worker.Process(func(job *bullmq.Job) error {
		started <- job.ID
		time.Sleep(300 * time.Millisecond)
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for job pickup
	select {
	case id := <-started:
		assert.Equal(t, jobID, id)

		// Verify job moved to active
		activeKey := "bull:{" + queueName + "}:active"
		activeLen, _ := rdb.LLen(ctx, activeKey).Result()
		assert.Equal(t, int64(1), activeLen, "Job should be in active queue")

		// Verify job removed from wait
		waitLen, _ := rdb.LLen(ctx, waitKey).Result()
		assert.Equal(t, int64(0), waitLen, "Job should be removed from wait queue")

	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for job pickup")
	}
}

// T044: Lock has correct TTL (30s)
func TestWorker_LockTTL(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-ttl-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Create worker with 30s lock duration
	opts := bullmq.DefaultWorkerOptions
	opts.LockDuration = 30 * time.Second
	worker := bullmq.NewWorker(queueName, rdb, opts)
	started := make(chan string, 1)

	worker.Process(func(job *bullmq.Job) error {
		started <- job.ID
		time.Sleep(500 * time.Millisecond)
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for job pickup
	select {
	case <-started:
		// Check lock TTL
		lockKey := "bull:{" + queueName + "}:" + job.ID + ":lock"
		ttl, err := rdb.TTL(ctx, lockKey).Result()
		require.NoError(t, err)

		// TTL should be ~30s (allow 2s margin for processing delay)
		assert.GreaterOrEqual(t, ttl.Seconds(), 28.0, "Lock TTL should be at least 28s")
		assert.LessOrEqual(t, ttl.Seconds(), 30.0, "Lock TTL should be at most 30s")

	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for job pickup")
	}
}

// T055: Job moves active→completed with result
func TestWorker_MoveToCompleted(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-completed-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)
	jobID := job.ID

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	worker.Process(func(job *bullmq.Job) error {
		return nil // Success
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for completion
	time.Sleep(1 * time.Second)

	// Verify job in completed set
	completedKey := "bull:{" + queueName + "}:completed"
	score, err := rdb.ZScore(ctx, completedKey, jobID).Result()
	require.NoError(t, err)
	assert.Greater(t, score, float64(0), "Job should be in completed set with timestamp")

	// Verify job removed from active
	activeKey := "bull:{" + queueName + "}:active"
	activeLen, _ := rdb.LLen(ctx, activeKey).Result()
	assert.Equal(t, int64(0), activeLen, "Job should be removed from active queue")
}

// T056: Job moves active→failed with error details
func TestWorker_MoveToFailed(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-failed-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{
		Attempts: 1, // Only 1 attempt to force immediate failure
	})
	require.NoError(t, err)
	jobID := job.ID

	// Create worker that fails
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	worker.Process(func(job *bullmq.Job) error {
		return &bullmq.PermanentError{Err: errors.New("test error")}
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for failure
	time.Sleep(1 * time.Second)

	// Verify job in failed set
	failedKey := "bull:{" + queueName + "}:failed"
	score, err := rdb.ZScore(ctx, failedKey, jobID).Result()
	require.NoError(t, err)
	assert.Greater(t, score, float64(0), "Job should be in failed set")

	// Verify failedReason stored
	jobKey := "bull:{" + queueName + "}:" + jobID
	failedReason, err := rdb.HGet(ctx, jobKey, "failedReason").Result()
	require.NoError(t, err)
	assert.Contains(t, failedReason, "test error")
}

// T057: Job removed after completion if removeOnComplete=true
func TestWorker_RemoveOnComplete(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-remove-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{
		RemoveOnComplete: true,
	})
	require.NoError(t, err)
	jobID := job.ID

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	worker.Process(func(job *bullmq.Job) error {
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for completion
	time.Sleep(1 * time.Second)

	// Verify job NOT in completed set
	completedKey := "bull:{" + queueName + "}:completed"
	exists, err := rdb.ZScore(ctx, completedKey, jobID).Result()
	assert.Error(t, err, "Job should NOT be in completed set")
	assert.Equal(t, float64(0), exists)

	// Verify job hash removed
	jobKey := "bull:{" + queueName + "}:" + jobID
	jobExists, _ := rdb.Exists(ctx, jobKey).Result()
	assert.Equal(t, int64(0), jobExists, "Job hash should be removed")
}
