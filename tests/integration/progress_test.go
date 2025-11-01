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

// T087: UpdateProgress stores progress in job hash
func TestProgress_StoresInJobHash(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-progress-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)
	jobID := job.ID

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)

	progressUpdated := make(chan bool, 1)
	worker.Process(func(job *bullmq.Job) error {
		// Update progress to 50%
		job.UpdateProgress(50)
		progressUpdated <- true

		time.Sleep(200 * time.Millisecond)
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for progress update
	<-progressUpdated

	// Verify progress stored in job hash
	jobKey := "bull:{" + queueName + "}:" + jobID
	progress, err := rdb.HGet(ctx, jobKey, "progress").Result()
	require.NoError(t, err)
	assert.Equal(t, "50", progress, "Progress should be stored in job hash")
}

// T088: UpdateProgress emits "progress" event
func TestProgress_EmitsProgressEvent(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-progress-event-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	_, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)

	worker.Process(func(job *bullmq.Job) error {
		job.UpdateProgress(75)
		time.Sleep(200 * time.Millisecond)
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for processing
	time.Sleep(2 * time.Second)

	// Check events stream for "progress" event
	eventsKey := "bull:{" + queueName + "}:events"
	events, err := rdb.XRange(ctx, eventsKey, "-", "+").Result()
	require.NoError(t, err)

	// Look for progress event
	found := false
	for _, event := range events {
		if eventType, ok := event.Values["event"].(string); ok && eventType == "progress" {
			found = true
			break
		}
	}

	assert.True(t, found, "Progress event should be emitted to events stream")
}

// T089: Log() appends entry to job logs list
func TestProgress_AppendsLog(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-log-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)
	jobID := job.ID

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)

	logAdded := make(chan bool, 1)
	worker.Process(func(job *bullmq.Job) error {
		job.Log("Processing started")
		job.Log("Step 1 complete")
		logAdded <- true

		time.Sleep(200 * time.Millisecond)
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for log
	<-logAdded

	// Verify logs stored
	logsKey := "bull:{" + queueName + "}:" + jobID + ":logs"
	logs, err := rdb.LRange(ctx, logsKey, 0, -1).Result()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(logs), 2, "At least 2 log entries should exist")

	// Verify log content
	assert.Contains(t, logs[0], "Processing started")
	assert.Contains(t, logs[1], "Step 1 complete")
}

// T090: Log list trimmed to max 1000 entries
func TestProgress_LogTrimmedTo1000(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-log-trim-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)
	jobID := job.ID

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)

	worker.Process(func(job *bullmq.Job) error {
		// Add 1500 log entries
		for i := 0; i < 1500; i++ {
			job.Log("Log entry " + string(rune(i)))
		}
		return nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for processing
	time.Sleep(3 * time.Second)

	// Verify logs trimmed to max 1000
	logsKey := "bull:{" + queueName + "}:" + jobID + ":logs"
	logCount, err := rdb.LLen(ctx, logsKey).Result()
	require.NoError(t, err)
	assert.LessOrEqual(t, logCount, int64(1000), "Log list should be trimmed to max 1000 entries")
}
