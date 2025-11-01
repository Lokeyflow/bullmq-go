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

func TestQueue_RemoveJob(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	ctx := context.Background()
	queue := bullmq.NewQueue("test-remove", client)

	// Add job
	job, err := queue.Add(ctx, "test", map[string]interface{}{"data": "value"}, bullmq.DefaultJobOptions)
	require.NoError(t, err)

	// Verify job exists
	retrievedJob, err := queue.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, job.ID, retrievedJob.ID)

	// Remove job
	err = queue.RemoveJob(ctx, job.ID)
	require.NoError(t, err)

	// Verify job no longer exists
	_, err = queue.GetJob(ctx, job.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestQueue_Clean_Completed(t *testing.T) {
	if testing.Short() {
		t.Skip("Integration test")
	}

	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	ctx := context.Background()
	queueName := "test-clean-completed"
	kb := bullmq.NewKeyBuilder(queueName)

	// Clean up
	client.Del(ctx, kb.Completed())

	queue := bullmq.NewQueue(queueName, client)

	// Add old completed job (timestamp 2 hours ago)
	oldJobID := "old-job-1"
	twoHoursAgo := time.Now().Add(-2 * time.Hour).UnixMilli()
	client.ZAdd(ctx, kb.Completed(), redis.Z{
		Score:  float64(twoHoursAgo),
		Member: oldJobID,
	})

	// Add recent completed job (timestamp 30 minutes ago)
	recentJobID := "recent-job-1"
	thirtyMinAgo := time.Now().Add(-30 * time.Minute).UnixMilli()
	client.ZAdd(ctx, kb.Completed(), redis.Z{
		Score:  float64(thirtyMinAgo),
		Member: recentJobID,
	})

	// Clean jobs older than 1 hour
	cleaned, err := queue.Clean(ctx, 1*time.Hour, 0, "completed")
	require.NoError(t, err)
	assert.Equal(t, 1, cleaned)

	// Verify old job removed
	exists := client.ZScore(ctx, kb.Completed(), oldJobID).Val()
	assert.Equal(t, float64(0), exists)

	// Verify recent job still exists
	exists = client.ZScore(ctx, kb.Completed(), recentJobID).Val()
	assert.NotEqual(t, float64(0), exists)
}

func TestQueue_RetryJob(t *testing.T) {
	if testing.Short() {
		t.Skip("Integration test")
	}

	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	ctx := context.Background()
	queueName := "test-retry"
	kb := bullmq.NewKeyBuilder(queueName)

	// Clean up
	client.Del(ctx, kb.Failed(), kb.Wait())

	queue := bullmq.NewQueue(queueName, client)

	// Create a failed job
	job, err := queue.Add(ctx, "test", map[string]interface{}{"retry": true}, bullmq.DefaultJobOptions)
	require.NoError(t, err)

	// Move to failed manually
	client.ZAdd(ctx, kb.Failed(), redis.Z{
		Score:  float64(time.Now().UnixMilli()),
		Member: job.ID,
	})
	client.HSet(ctx, kb.Job(job.ID), "failedReason", "test failure")

	// Retry the job
	err = queue.RetryJob(ctx, job.ID)
	require.NoError(t, err)

	// Verify job moved to wait queue
	waitJobs, _ := queue.GetWaitingJobs(ctx, 0, -1)
	assert.Contains(t, waitJobs, job.ID)

	// Verify removed from failed queue
	failedJobs, _ := queue.GetFailedJobs(ctx, 0, -1)
	assert.NotContains(t, failedJobs, job.ID)
}

func TestQueue_GetJobs(t *testing.T) {
	if testing.Short() {
		t.Skip("Integration test")
	}

	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	ctx := context.Background()
	queue := bullmq.NewQueue("test-get-jobs", client)

	// Add multiple jobs
	job1, _ := queue.Add(ctx, "job1", map[string]interface{}{"index": 1}, bullmq.DefaultJobOptions)
	job2, _ := queue.Add(ctx, "job2", map[string]interface{}{"index": 2}, bullmq.DefaultJobOptions)

	// Retrieve jobs
	jobs, err := queue.GetJobs(ctx, []string{job1.ID, job2.ID})
	require.NoError(t, err)
	assert.Len(t, jobs, 2)

	// Verify job data
	foundJob1 := false
	foundJob2 := false
	for _, job := range jobs {
		if job.ID == job1.ID {
			foundJob1 = true
			assert.Equal(t, "job1", job.Name)
		}
		if job.ID == job2.ID {
			foundJob2 = true
			assert.Equal(t, "job2", job.Name)
		}
	}
	assert.True(t, foundJob1)
	assert.True(t, foundJob2)
}

func TestQueue_GetJobsByState(t *testing.T) {
	if testing.Short() {
		t.Skip("Integration test")
	}

	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	ctx := context.Background()
	queueName := "test-get-by-state"
	kb := bullmq.NewKeyBuilder(queueName)

	// Clean up
	client.Del(ctx, kb.Wait(), kb.Completed())

	queue := bullmq.NewQueue(queueName, client)

	// Add waiting jobs
	job1, _ := queue.Add(ctx, "waiting1", map[string]interface{}{}, bullmq.DefaultJobOptions)
	job2, _ := queue.Add(ctx, "waiting2", map[string]interface{}{}, bullmq.DefaultJobOptions)

	// Get waiting jobs
	waitingJobs, err := queue.GetWaitingJobs(ctx, 0, -1)
	require.NoError(t, err)
	assert.Contains(t, waitingJobs, job1.ID)
	assert.Contains(t, waitingJobs, job2.ID)

	// Add completed job manually
	completedJobID := "completed-1"
	client.ZAdd(ctx, kb.Completed(), redis.Z{
		Score:  float64(time.Now().UnixMilli()),
		Member: completedJobID,
	})

	// Get completed jobs
	completedJobs, err := queue.GetCompletedJobs(ctx, 0, -1)
	require.NoError(t, err)
	assert.Contains(t, completedJobs, completedJobID)
}
