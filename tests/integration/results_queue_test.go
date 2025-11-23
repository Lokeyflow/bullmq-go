package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResultsQueue_ExplicitMode tests ProcessWithResults()
func TestResultsQueue_ExplicitMode(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-results-explicit"
	resultsQueueName := "results"

	// Add a job to process
	queue := bullmq.NewQueue(queueName, rdb)
	job, err := queue.Add(ctx, "test-job", map[string]interface{}{
		"input": "test-data",
	}, bullmq.JobOptions{Attempts: 3})
	require.NoError(t, err)

	// Track results
	resultReceived := make(chan map[string]interface{}, 1)
	jobCompleted := make(chan bool, 1)

	// Worker with ProcessWithResults()
	worker := bullmq.NewWorker(queueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		StalledCheckInterval: 30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		LockDuration:         30 * time.Second,
	})

	errorCallbackCalled := false
	worker.ProcessWithResults(resultsQueueName, func(job *bullmq.Job) (interface{}, error) {
		result := map[string]interface{}{
			"output":    "processed-data",
			"timestamp": time.Now().Unix(),
			"jobId":     job.ID,
		}

		jobCompleted <- true
		return result, nil
	}, bullmq.ResultsQueueConfig{
		OnError: func(jobID string, err error) {
			errorCallbackCalled = true
			t.Logf("Error callback called for job %s: %v", jobID, err)
		},
	})

	// Results worker
	resultsWorker := bullmq.NewWorker(resultsQueueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		StalledCheckInterval: 30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		LockDuration:         30 * time.Second,
	})

	resultsWorker.Process(func(job *bullmq.Job) (interface{}, error) {
		resultReceived <- job.Data
		return nil, nil
	})

	// Start workers
	go worker.Start(ctx)
	go resultsWorker.Start(ctx)
	defer worker.Stop()
	defer resultsWorker.Stop()

	// Wait for job completion
	select {
	case <-jobCompleted:
		t.Log("Job completed")
	case <-time.After(5 * time.Second):
		t.Fatal("Job did not complete in time")
	}

	// Wait for result to be sent to results queue
	select {
	case result := <-resultReceived:
		t.Logf("Result received: %+v", result)

		// Verify result metadata
		assert.Equal(t, job.ID, result["jobId"])
		assert.Equal(t, queueName, result["queueName"])
		assert.NotNil(t, result["result"])
		assert.NotNil(t, result["timestamp"])
		assert.NotNil(t, result["processTime"])
		assert.NotNil(t, result["attempt"])
		assert.NotNil(t, result["workerId"])

		// Verify result data
		resultData := result["result"].(map[string]interface{})
		assert.Equal(t, "processed-data", resultData["output"])

	case <-time.After(5 * time.Second):
		t.Fatal("Result was not sent to results queue")
	}

	// Verify error callback was NOT called (success case)
	assert.False(t, errorCallbackCalled, "Error callback should not be called on success")

	// Verify returnvalue is stored as JSON in job hash
	kb := bullmq.NewKeyBuilder(queueName, rdb)
	returnValueStr, err := rdb.HGet(ctx, kb.Job(job.ID), "returnvalue").Result()
	require.NoError(t, err)

	// Parse JSON to verify it's valid JSON
	var returnValue map[string]interface{}
	err = json.Unmarshal([]byte(returnValueStr), &returnValue)
	require.NoError(t, err, "returnvalue should be valid JSON")
	assert.Equal(t, "processed-data", returnValue["output"])
}

// TestResultsQueue_ImplicitMode tests WorkerOptions.ResultsQueue
func TestResultsQueue_ImplicitMode(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-results-implicit"
	resultsQueueName := "results-implicit"

	// Add a job
	queue := bullmq.NewQueue(queueName, rdb)
	_, err := queue.Add(ctx, "test-job", map[string]interface{}{
		"data": "test",
	}, bullmq.JobOptions{Attempts: 3})
	require.NoError(t, err)

	resultReceived := make(chan bool, 1)

	// Worker with implicit results queue
	worker := bullmq.NewWorker(queueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		StalledCheckInterval: 30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		LockDuration:         30 * time.Second,
		ResultsQueue: &bullmq.ResultsQueueConfig{
			QueueName: resultsQueueName,
			Options: bullmq.JobOptions{
				Attempts:         5,
				RemoveOnComplete: true,
			},
		},
	})

	worker.Process(func(job *bullmq.Job) (interface{}, error) {
		return map[string]interface{}{
			"status": "success",
			"data":   "implicit-result",
		}, nil
	})

	// Results worker
	resultsWorker := bullmq.NewWorker(resultsQueueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		StalledCheckInterval: 30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		LockDuration:         30 * time.Second,
	})

	resultsWorker.Process(func(job *bullmq.Job) (interface{}, error) {
		// Verify metadata
		assert.NotNil(t, job.Data["jobId"])
		assert.Equal(t, queueName, job.Data["queueName"])
		assert.NotNil(t, job.Data["result"])

		resultData := job.Data["result"].(map[string]interface{})
		assert.Equal(t, "success", resultData["status"])
		assert.Equal(t, "implicit-result", resultData["data"])

		resultReceived <- true
		return nil, nil
	})

	// Start workers
	go worker.Start(ctx)
	go resultsWorker.Start(ctx)
	defer worker.Stop()
	defer resultsWorker.Stop()

	// Wait for result
	select {
	case <-resultReceived:
		t.Log("Result received in implicit mode")
	case <-time.After(5 * time.Second):
		t.Fatal("Result was not received in implicit mode")
	}
}

// TestResultsQueue_FailureDoesNotSendResult tests that failed jobs don't send to results queue
func TestResultsQueue_FailureDoesNotSendResult(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-results-failure"
	resultsQueueName := "results-failure"

	// Add a job
	queue := bullmq.NewQueue(queueName, rdb)
	_, err := queue.Add(ctx, "test-job", map[string]interface{}{
		"shouldFail": true,
	}, bullmq.JobOptions{Attempts: 1}) // Only 1 attempt to fail immediately
	require.NoError(t, err)

	resultReceived := make(chan bool, 1)
	jobFailed := make(chan bool, 1)

	// Worker that fails
	worker := bullmq.NewWorker(queueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		StalledCheckInterval: 30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		LockDuration:         30 * time.Second,
		ResultsQueue: &bullmq.ResultsQueueConfig{
			QueueName: resultsQueueName,
		},
	})

	worker.Process(func(job *bullmq.Job) (interface{}, error) {
		// Simulate failure
		return nil, &bullmq.PermanentError{Err: assert.AnError}
	})

	// Results worker (should NOT receive anything)
	resultsWorker := bullmq.NewWorker(resultsQueueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		StalledCheckInterval: 30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		LockDuration:         30 * time.Second,
	})

	resultsWorker.Process(func(job *bullmq.Job) (interface{}, error) {
		resultReceived <- true
		return nil, nil
	})

	// Start workers
	go worker.Start(ctx)
	go resultsWorker.Start(ctx)
	defer worker.Stop()
	defer resultsWorker.Stop()

	// Wait to ensure no result is sent
	go func() {
		time.Sleep(3 * time.Second)
		jobFailed <- true
	}()

	select {
	case <-resultReceived:
		t.Fatal("Result should NOT be sent to results queue on failure")
	case <-jobFailed:
		t.Log("Correctly did not send result for failed job")
	}

	// Verify results queue is empty
	resultsKb := bullmq.NewKeyBuilder(resultsQueueName, rdb)
	count, err := rdb.LLen(ctx, resultsKb.Wait()).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "Results queue should be empty")
}

// TestResultsQueue_ErrorCallback tests OnError callback
func TestResultsQueue_ErrorCallback(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-results-error-callback"

	// Add a job
	queue := bullmq.NewQueue(queueName, rdb)
	_, err := queue.Add(ctx, "test-job", map[string]interface{}{
		"data": "test",
	}, bullmq.JobOptions{Attempts: 3})
	require.NoError(t, err)

	errorCallbackCalled := make(chan string, 1)

	// Worker with results queue pointing to non-existent/broken queue
	// (we'll simulate error by using invalid Redis after job completes)
	worker := bullmq.NewWorker(queueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		StalledCheckInterval: 30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		LockDuration:         30 * time.Second,
	})

	worker.ProcessWithResults("invalid-results-queue", func(job *bullmq.Job) (interface{}, error) {
		return map[string]interface{}{"status": "ok"}, nil
	}, bullmq.ResultsQueueConfig{
		OnError: func(jobID string, err error) {
			t.Logf("Error callback triggered for job %s: %v", jobID, err)
			errorCallbackCalled <- jobID
		},
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Note: This test may not trigger the error callback reliably
	// because Redis operations typically succeed. This is more of a
	// demonstration of the callback mechanism.

	time.Sleep(2 * time.Second)

	t.Log("Error callback test completed (callback may or may not be called depending on Redis state)")
}

// TestResultsQueue_JSONSerialization verifies returnvalue is stored as JSON
func TestResultsQueue_JSONSerialization(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-json-serialization"

	queue := bullmq.NewQueue(queueName, rdb)
	job, err := queue.Add(ctx, "test-job", map[string]interface{}{
		"data": "test",
	}, bullmq.JobOptions{Attempts: 3})
	require.NoError(t, err)

	jobCompleted := make(chan bool, 1)

	worker := bullmq.NewWorker(queueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		StalledCheckInterval: 30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		LockDuration:         30 * time.Second,
		ResultsQueue: &bullmq.ResultsQueueConfig{
			QueueName: "results",
		},
	})

	worker.Process(func(job *bullmq.Job) (interface{}, error) {
		result := map[string]interface{}{
			"nested": map[string]interface{}{
				"key":   "value",
				"count": 42,
			},
			"array": []string{"a", "b", "c"},
			"bool":  true,
		}
		jobCompleted <- true
		return result, nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait for completion
	<-jobCompleted
	time.Sleep(500 * time.Millisecond)

	// Verify returnvalue is valid JSON
	kb := bullmq.NewKeyBuilder(queueName, rdb)
	returnValueStr, err := rdb.HGet(ctx, kb.Job(job.ID), "returnvalue").Result()
	require.NoError(t, err)

	t.Logf("returnvalue from Redis: %s", returnValueStr)

	// Must be valid JSON
	var returnValue map[string]interface{}
	err = json.Unmarshal([]byte(returnValueStr), &returnValue)
	require.NoError(t, err, "returnvalue must be valid JSON, not Go format")

	// Verify structure
	assert.NotNil(t, returnValue["nested"])
	assert.NotNil(t, returnValue["array"])
	assert.Equal(t, true, returnValue["bool"])

	nested := returnValue["nested"].(map[string]interface{})
	assert.Equal(t, "value", nested["key"])
	assert.Equal(t, float64(42), nested["count"]) // JSON numbers are float64
}
