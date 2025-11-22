package integration

import (
	"context"
	"testing"
	"time"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T096: Pause queue stops job processing
func TestQueue_PauseStopsProcessing(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-pause-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	// Add job before pause
	_, err := queue.Add(ctx, "test-job-1", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Pause queue
	require.NoError(t, queue.Pause(ctx))

	// Add job after pause
	_, err = queue.Add(ctx, "test-job-2", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processed := make(chan string, 2)

	worker.Process(func(job *bullmq.Job) (interface{}, error) {
		processed <- job.Name
		return nil, nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Wait - no jobs should be processed
	select {
	case <-processed:
		t.Fatal("Jobs should not be processed when queue is paused")
	case <-time.After(2 * time.Second):
		// Expected - no processing
	}
}

// T097: Resume queue restarts job processing
func TestQueue_ResumeRestartsProcessing(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-resume-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	// Pause queue
	require.NoError(t, queue.Pause(ctx))

	// Add job
	_, err := queue.Add(ctx, "test-job", map[string]interface{}{"task": "test"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Create worker
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processed := make(chan string, 1)

	worker.Process(func(job *bullmq.Job) (interface{}, error) {
		processed <- job.Name
		return nil, nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Resume queue
	time.Sleep(500 * time.Millisecond)
	require.NoError(t, queue.Resume(ctx))

	// Job should now be processed
	select {
	case name := <-processed:
		assert.Equal(t, "test-job", name)
	case <-time.After(3 * time.Second):
		t.Fatal("Job should be processed after queue resume")
	}
}

// T098: Clean removes old completed jobs
func TestQueue_CleanRemovesOldJobs(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-clean-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	// Add and complete jobs
	for i := 0; i < 5; i++ {
		jobID, _ := queue.Add(ctx, "test-job", map[string]interface{}{"index": i}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})

		// Manually move to completed with old timestamp
		completedKey := "bull:{" + queueName + "}:completed"
		oldTimestamp := time.Now().Add(-2 * time.Hour).UnixMilli()
		rdb.ZAdd(ctx, completedKey, redis.Z{Score: float64(oldTimestamp), Member: jobID})
	}

	// Verify 5 completed jobs
	completedKey := "bull:{" + queueName + "}:completed"
	count, _ := rdb.ZCard(ctx, completedKey).Result()
	assert.Equal(t, int64(5), count)

	// Clean jobs older than 1 hour
	removed, err := queue.Clean(ctx, 1*time.Hour, 100, "completed")
	require.NoError(t, err)
	assert.Equal(t, int64(5), removed, "All 5 old jobs should be cleaned")

	// Verify completed set empty
	count, _ = rdb.ZCard(ctx, completedKey).Result()
	assert.Equal(t, int64(0), count)
}

// T099: GetJobCounts returns accurate counts
func TestQueue_GetJobCountsAccurate(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-counts-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	// Add jobs
	queue.Add(ctx, "job-1", map[string]interface{}{}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	queue.Add(ctx, "job-2", map[string]interface{}{}, bullmq.JobOptions{Priority: 5})
	queue.Add(ctx, "job-3", map[string]interface{}{}, bullmq.JobOptions{Delay: 5 * time.Second})

	// Get counts
	counts, err := queue.GetJobCounts(ctx)
	require.NoError(t, err)

	assert.Equal(t, int64(1), counts.Waiting, "1 job in wait queue")
	assert.Equal(t, int64(1), counts.Prioritized, "1 job in prioritized queue")
	assert.Equal(t, int64(1), counts.Delayed, "1 job in delayed queue")
	assert.Equal(t, int64(0), counts.Active)
	assert.Equal(t, int64(0), counts.Completed)
	assert.Equal(t, int64(0), counts.Failed)
}

// T100: GetJob retrieves job by ID
func TestQueue_GetJobByID(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-getjob-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	addedJob, err := queue.Add(ctx, "test-job", map[string]interface{}{"foo": "bar"}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Get job
	job, err := queue.GetJob(ctx, addedJob.ID)
	require.NoError(t, err)
	require.NotNil(t, job)

	assert.Equal(t, addedJob.ID, job.ID)
	assert.Equal(t, "test-job", job.Name)
	assert.Equal(t, "bar", job.Data["foo"])
}

// T101: RemoveJob deletes job from queue
func TestQueue_RemoveJobDeletes(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-remove-queue"
	queue := bullmq.NewQueue(queueName, rdb)

	addedJob, err := queue.Add(ctx, "test-job", map[string]interface{}{}, bullmq.JobOptions{Attempts: 3, Backoff: bullmq.BackoffConfig{Type: "exponential", Delay: 1000}})
	require.NoError(t, err)

	// Verify job exists
	job, err := queue.GetJob(ctx, addedJob.ID)
	require.NoError(t, err)
	require.NotNil(t, job)

	// Remove job
	err = queue.RemoveJob(ctx, addedJob.ID)
	require.NoError(t, err)

	// Verify job removed
	job, err = queue.GetJob(ctx, addedJob.ID)
	assert.Error(t, err, "Job should not exist after removal")
	assert.Nil(t, job)

	// Verify removed from wait queue
	waitKey := "bull:{" + queueName + "}:wait"
	waitLen, _ := rdb.LLen(ctx, waitKey).Result()
	assert.Equal(t, int64(0), waitLen)
}

// T108: Drain removes all jobs from all queues
func TestQueue_DrainRemovesAllJobs(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-drain-queue"
	queue := bullmq.NewQueue(queueName, rdb)
	kb := bullmq.NewKeyBuilder(queueName, rdb)

	// Add jobs to different queues
	// 1. Wait queue (priority=0, no delay)
	job1, err := queue.Add(ctx, "wait-job", map[string]interface{}{"type": "wait"}, bullmq.JobOptions{})
	require.NoError(t, err)

	// 2. Prioritized queue (priority>0)
	job2, err := queue.Add(ctx, "priority-job", map[string]interface{}{"type": "priority"}, bullmq.JobOptions{Priority: 10})
	require.NoError(t, err)

	// 3. Delayed queue (delay>0)
	job3, err := queue.Add(ctx, "delayed-job", map[string]interface{}{"type": "delayed"}, bullmq.JobOptions{Delay: 60 * time.Second})
	require.NoError(t, err)

	// 4. Manually add jobs to completed and failed queues to test those as well
	job4ID := "completed-job-id"
	job5ID := "failed-job-id"

	// Create job hashes for completed/failed jobs
	rdb.HSet(ctx, kb.Job(job4ID), "id", job4ID, "name", "completed-job", "data", "{}")
	rdb.HSet(ctx, kb.Job(job5ID), "id", job5ID, "name", "failed-job", "data", "{}")

	// Add to completed and failed queues
	rdb.ZAdd(ctx, kb.Completed(), redis.Z{Score: float64(time.Now().UnixMilli()), Member: job4ID})
	rdb.ZAdd(ctx, kb.Failed(), redis.Z{Score: float64(time.Now().UnixMilli()), Member: job5ID})

	// Add logs to one of the jobs
	logsKey := kb.Logs(job1.ID)
	rdb.RPush(ctx, logsKey, "log entry 1", "log entry 2")

	// Verify jobs exist before drain
	counts, err := queue.GetJobCounts(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts.Waiting, "Should have 1 waiting job")
	assert.Equal(t, int64(1), counts.Prioritized, "Should have 1 prioritized job")
	assert.Equal(t, int64(1), counts.Delayed, "Should have 1 delayed job")
	assert.Equal(t, int64(1), counts.Completed, "Should have 1 completed job")
	assert.Equal(t, int64(1), counts.Failed, "Should have 1 failed job")

	// Drain queue
	removed, err := queue.Drain(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, removed, "Should remove all 5 jobs (3 added via queue.Add + 2 manually added)")

	// Verify all queues are empty
	counts, err = queue.GetJobCounts(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), counts.Waiting, "Wait queue should be empty")
	assert.Equal(t, int64(0), counts.Prioritized, "Prioritized queue should be empty")
	assert.Equal(t, int64(0), counts.Delayed, "Delayed queue should be empty")
	assert.Equal(t, int64(0), counts.Completed, "Completed queue should be empty")
	assert.Equal(t, int64(0), counts.Failed, "Failed queue should be empty")
	assert.Equal(t, int64(0), counts.Active, "Active queue should be empty")

	// Verify job hashes are removed
	job, err := queue.GetJob(ctx, job1.ID)
	assert.Error(t, err, "Job1 hash should not exist")
	assert.Nil(t, job)

	job, err = queue.GetJob(ctx, job2.ID)
	assert.Error(t, err, "Job2 hash should not exist")
	assert.Nil(t, job)

	job, err = queue.GetJob(ctx, job3.ID)
	assert.Error(t, err, "Job3 hash should not exist")
	assert.Nil(t, job)

	job, err = queue.GetJob(ctx, job4ID)
	assert.Error(t, err, "Job4 hash should not exist")
	assert.Nil(t, job)

	job, err = queue.GetJob(ctx, job5ID)
	assert.Error(t, err, "Job5 hash should not exist")
	assert.Nil(t, job)

	// Verify logs are removed
	logsLen, _ := rdb.LLen(ctx, logsKey).Result()
	assert.Equal(t, int64(0), logsLen, "Job logs should be removed")

	// Verify events stream is cleared
	eventsKey := kb.Events()
	eventsLen, _ := rdb.XLen(ctx, eventsKey).Result()
	assert.Equal(t, int64(0), eventsLen, "Events stream should be cleared")
}
