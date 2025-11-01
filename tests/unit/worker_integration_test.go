// +build integration

package unit

import (
	"context"
	"testing"
	"time"

	"github.com/Lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorker_ProcessJob_EndToEnd tests full job lifecycle
func TestWorker_ProcessJob_EndToEnd(t *testing.T) {
	// Setup Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	ctx := context.Background()

	// Flush test queue
	queueName := "test-worker-e2e"
	kb := bullmq.NewKeyBuilder(queueName)
	client.Del(ctx, kb.Wait(), kb.Active(), kb.Completed(), kb.Failed())

	// Create queue and add job
	queue := bullmq.NewQueue(queueName, client)
	job, err := queue.Add(ctx, "test-task", map[string]interface{}{
		"input": "hello",
	}, bullmq.DefaultJobOptions)
	require.NoError(t, err)
	assert.NotEmpty(t, job.ID)

	// Create worker
	processedJobs := make(chan string, 1)
	worker := bullmq.NewWorker(queueName, client, bullmq.WorkerOptions{
		Concurrency: 1,
	})

	worker.Process(func(j *bullmq.Job) error {
		processedJobs <- j.ID
		return nil
	})

	// Start worker in background
	workerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	go worker.Start(workerCtx)

	// Wait for job to be processed
	select {
	case processedID := <-processedJobs:
		assert.Equal(t, job.ID, processedID)
	case <-time.After(3 * time.Second):
		t.Fatal("Job not processed within timeout")
	}

	// Verify job moved to completed
	time.Sleep(500 * time.Millisecond) // Allow cleanup
	completedCount, _ := client.ZCard(ctx, kb.Completed()).Result()
	assert.Equal(t, int64(1), completedCount)

	// Cleanup
	worker.Stop()
}

// TestWorker_JobFailure_MovesToFailed tests failed job handling
func TestWorker_JobFailure_MovesToFailed(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	ctx := context.Background()
	queueName := "test-worker-fail"
	kb := bullmq.NewKeyBuilder(queueName)
	client.Del(ctx, kb.Wait(), kb.Active(), kb.Completed(), kb.Failed())

	// Add job
	queue := bullmq.NewQueue(queueName, client)
	job, err := queue.Add(ctx, "fail-task", map[string]interface{}{
		"action": "fail",
	}, bullmq.JobOptions{
		Attempts: 1, // No retries
	})
	require.NoError(t, err)

	// Create worker that fails
	processedCount := 0
	worker := bullmq.NewWorker(queueName, client, bullmq.WorkerOptions{
		Concurrency: 1,
		MaxAttempts: 1,
	})

	worker.Process(func(j *bullmq.Job) error {
		processedCount++
		return &bullmq.PermanentError{Message: "intentional failure"}
	})

	// Start worker
	workerCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	go worker.Start(workerCtx)

	// Wait for processing
	time.Sleep(2 * time.Second)

	// Verify job in failed queue
	failedCount, _ := client.ZCard(ctx, kb.Failed()).Result()
	assert.Equal(t, int64(1), failedCount)

	// Verify failure reason stored
	failedReason, _ := client.HGet(ctx, kb.Job(job.ID), "failedReason").Result()
	assert.Contains(t, failedReason, "intentional failure")

	worker.Stop()
}

// TestWorker_Concurrency tests parallel job processing
func TestWorker_Concurrency(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	ctx := context.Background()
	queueName := "test-worker-concurrent"
	kb := bullmq.NewKeyBuilder(queueName)
	client.Del(ctx, kb.Wait(), kb.Active(), kb.Completed(), kb.Failed())

	// Add multiple jobs
	queue := bullmq.NewQueue(queueName, client)
	jobCount := 5
	for i := 0; i < jobCount; i++ {
		_, err := queue.Add(ctx, "concurrent-task", map[string]interface{}{
			"index": i,
		}, bullmq.DefaultJobOptions)
		require.NoError(t, err)
	}

	// Create worker with concurrency
	processedJobs := make(chan int, jobCount)
	worker := bullmq.NewWorker(queueName, client, bullmq.WorkerOptions{
		Concurrency: 3,
	})

	worker.Process(func(j *bullmq.Job) error {
		time.Sleep(100 * time.Millisecond) // Simulate work
		processedJobs <- 1
		return nil
	})

	// Start worker
	workerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	go worker.Start(workerCtx)

	// Wait for all jobs
	processed := 0
	timeout := time.After(5 * time.Second)
	for processed < jobCount {
		select {
		case <-processedJobs:
			processed++
		case <-timeout:
			t.Fatalf("Only processed %d/%d jobs", processed, jobCount)
		}
	}

	assert.Equal(t, jobCount, processed)
	worker.Stop()
}

// TestWorker_PriorityProcessing tests priority queue handling
func TestWorker_PriorityProcessing(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer client.Close()

	ctx := context.Background()
	queueName := "test-worker-priority"
	kb := bullmq.NewKeyBuilder(queueName)
	client.Del(ctx, kb.Wait(), kb.Prioritized(), kb.Active(), kb.Completed())

	queue := bullmq.NewQueue(queueName, client)

	// Add low priority job first
	_, err := queue.Add(ctx, "low-priority", map[string]interface{}{
		"priority": 1,
	}, bullmq.JobOptions{
		Priority: 1,
	})
	require.NoError(t, err)

	// Add high priority job second
	highPrioJob, err := queue.Add(ctx, "high-priority", map[string]interface{}{
		"priority": 10,
	}, bullmq.JobOptions{
		Priority: 10,
	})
	require.NoError(t, err)

	// Create worker
	processedOrder := make([]string, 0)
	worker := bullmq.NewWorker(queueName, client, bullmq.WorkerOptions{
		Concurrency: 1, // Sequential processing
	})

	worker.Process(func(j *bullmq.Job) error {
		processedOrder = append(processedOrder, j.ID)
		return nil
	})

	// Start worker
	workerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	go worker.Start(workerCtx)

	// Wait for processing
	time.Sleep(2 * time.Second)

	// High priority job should be processed first
	require.GreaterOrEqual(t, len(processedOrder), 1)
	assert.Equal(t, highPrioJob.ID, processedOrder[0])

	worker.Stop()
}
