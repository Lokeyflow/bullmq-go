package bullmq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Add submits a new job to the queue
func (q *Queue) Add(ctx context.Context, name string, data map[string]interface{}, opts JobOptions) (*Job, error) {
	// Validate job options
	if err := ValidateJobOptions(opts); err != nil {
		return nil, err
	}

	// Generate job ID
	jobID := uuid.New().String()

	// Create job instance
	job := &Job{
		ID:           jobID,
		Name:         name,
		Data:         data,
		Opts:         opts,
		Progress:     0,
		Timestamp:    time.Now().UnixMilli(),
		AttemptsMade: 0,
		Delay:        int64(opts.Delay.Milliseconds()),
	}

	// Validate payload size
	if err := ValidateJobPayloadSize(job); err != nil {
		return nil, err
	}

	// Store job hash in Redis
	if err := q.storeJobHash(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to store job: %w", err)
	}

	// Add job to appropriate queue
	if err := q.enqueueJob(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to enqueue job: %w", err)
	}

	// Emit "waiting" event
	if err := q.emitWaitingEvent(ctx, job); err != nil {
		// Log error but don't fail job submission
		// Event emission failure is not critical
	}

	return job, nil
}

// storeJobHash stores job data as a Redis hash
func (q *Queue) storeJobHash(ctx context.Context, job *Job) error {
	key := q.keyBuilder.Job(job.ID)

	// Serialize data and opts as JSON
	dataJSON, err := json.Marshal(job.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal job data: %w", err)
	}

	optsJSON, err := json.Marshal(job.Opts)
	if err != nil {
		return fmt.Errorf("failed to marshal job opts: %w", err)
	}

	// Store job hash
	fields := map[string]interface{}{
		"id":           job.ID,
		"name":         job.Name,
		"data":         string(dataJSON),
		"opts":         string(optsJSON),
		"progress":     job.Progress,
		"delay":        job.Delay,
		"timestamp":    job.Timestamp,
		"attemptsMade": job.AttemptsMade,
	}

	return q.redisClient.HSet(ctx, key, fields).Err()
}

// enqueueJob adds job to the appropriate queue based on options
func (q *Queue) enqueueJob(ctx context.Context, job *Job) error {
	// Delayed jobs go to delayed queue
	if job.Delay > 0 {
		delayedTimestamp := job.Timestamp + job.Delay
		return q.redisClient.ZAdd(ctx, q.keyBuilder.Delayed(), redis.Z{
			Score:  float64(delayedTimestamp),
			Member: job.ID,
		}).Err()
	}

	// Priority jobs go to prioritized queue
	if job.Opts.Priority > 0 {
		// Higher priority = lower score (processed first)
		priorityScore := float64(-job.Opts.Priority)
		return q.redisClient.ZAdd(ctx, q.keyBuilder.Prioritized(), redis.Z{
			Score:  priorityScore,
			Member: job.ID,
		}).Err()
	}

	// Default: add to wait queue (FIFO)
	return q.redisClient.LPush(ctx, q.keyBuilder.Wait(), job.ID).Err()
}

// emitWaitingEvent emits a "waiting" event to the events stream
func (q *Queue) emitWaitingEvent(ctx context.Context, job *Job) error {
	event := map[string]interface{}{
		"event":        EventWaiting,
		"jobId":        job.ID,
		"name":         job.Name,
		"timestamp":    time.Now().UnixMilli(),
		"attemptsMade": job.AttemptsMade,
		"priority":     job.Opts.Priority,
		"delay":        job.Delay,
	}

	// Add to events stream with max length
	return q.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: q.keyBuilder.Events(),
		MaxLen: 10000,
		Approx: true, // Use approximate trimming for performance
		Values: event,
	}).Err()
}

// GetJob retrieves a job by ID
func (q *Queue) GetJob(ctx context.Context, jobID string) (*Job, error) {
	key := q.keyBuilder.Job(jobID)

	// Get job hash
	result, err := q.redisClient.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	// Deserialize job
	job := &Job{ID: jobID}

	if name, ok := result["name"]; ok {
		job.Name = name
	}

	if dataJSON, ok := result["data"]; ok {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(dataJSON), &data); err == nil {
			job.Data = data
		}
	}

	// Add more field parsing as needed...

	return job, nil
}

// GetJobCounts returns counts of jobs in each state
func (q *Queue) GetJobCounts(ctx context.Context) (*JobCounts, error) {
	counts := &JobCounts{}

	// Use pipeline for parallel execution
	pipe := q.redisClient.Pipeline()

	waitCmd := pipe.LLen(ctx, q.keyBuilder.Wait())
	prioritizedCmd := pipe.ZCard(ctx, q.keyBuilder.Prioritized())
	delayedCmd := pipe.ZCard(ctx, q.keyBuilder.Delayed())
	activeCmd := pipe.LLen(ctx, q.keyBuilder.Active())
	completedCmd := pipe.ZCard(ctx, q.keyBuilder.Completed())
	failedCmd := pipe.ZCard(ctx, q.keyBuilder.Failed())

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}

	counts.Waiting, _ = waitCmd.Result()
	counts.Prioritized, _ = prioritizedCmd.Result()
	counts.Delayed, _ = delayedCmd.Result()
	counts.Active, _ = activeCmd.Result()
	counts.Completed, _ = completedCmd.Result()
	counts.Failed, _ = failedCmd.Result()

	return counts, nil
}

// Pause pauses the queue
func (q *Queue) Pause(ctx context.Context) error {
	return q.redisClient.HSet(ctx, q.keyBuilder.Meta(), "paused", "1").Err()
}

// Resume resumes the queue
func (q *Queue) Resume(ctx context.Context) error {
	return q.redisClient.HDel(ctx, q.keyBuilder.Meta(), "paused").Err()
}

// IsPaused checks if queue is paused
func (q *Queue) IsPaused(ctx context.Context) (bool, error) {
	val, err := q.redisClient.HGet(ctx, q.keyBuilder.Meta(), "paused").Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return val == "1", nil
}

// RemoveJob removes a job from the queue
func (q *Queue) RemoveJob(ctx context.Context, jobID string) error {
	kb := q.keyBuilder

	// Remove from all possible queues
	pipe := q.redisClient.Pipeline()
	pipe.LRem(ctx, kb.Wait(), 0, jobID)
	pipe.LRem(ctx, kb.Active(), 0, jobID)
	pipe.ZRem(ctx, kb.Prioritized(), jobID)
	pipe.ZRem(ctx, kb.Delayed(), jobID)
	pipe.ZRem(ctx, kb.Completed(), jobID)
	pipe.ZRem(ctx, kb.Failed(), jobID)

	// Delete job data and lock
	pipe.Del(ctx, kb.Job(jobID))
	pipe.Del(ctx, kb.Lock(jobID))
	pipe.Del(ctx, kb.Logs(jobID))

	_, err := pipe.Exec(ctx)
	return err
}

// Clean removes old jobs from completed or failed queues
// age: remove jobs older than this duration
// limit: maximum number of jobs to remove (0 = no limit)
// jobType: "completed" or "failed"
func (q *Queue) Clean(ctx context.Context, age time.Duration, limit int, jobType string) (int, error) {
	kb := q.keyBuilder

	var queueKey string
	switch jobType {
	case "completed":
		queueKey = kb.Completed()
	case "failed":
		queueKey = kb.Failed()
	default:
		return 0, fmt.Errorf("invalid job type: %s (must be 'completed' or 'failed')", jobType)
	}

	// Calculate cutoff timestamp
	cutoffTime := time.Now().Add(-age).UnixMilli()

	// Get jobs older than cutoff
	var jobIDs []string
	if limit > 0 {
		// Get limited number of old jobs
		results, err := q.redisClient.ZRangeByScoreWithScores(ctx, queueKey, &redis.ZRangeBy{
			Min:    "-inf",
			Max:    fmt.Sprintf("%d", cutoffTime),
			Offset: 0,
			Count:  int64(limit),
		}).Result()
		if err != nil {
			return 0, err
		}
		for _, z := range results {
			jobIDs = append(jobIDs, z.Member.(string))
		}
	} else {
		// Get all old jobs
		results, err := q.redisClient.ZRangeByScore(ctx, queueKey, &redis.ZRangeBy{
			Min: "-inf",
			Max: fmt.Sprintf("%d", cutoffTime),
		}).Result()
		if err != nil {
			return 0, err
		}
		jobIDs = results
	}

	if len(jobIDs) == 0 {
		return 0, nil
	}

	// Remove jobs
	pipe := q.redisClient.Pipeline()
	for _, jobID := range jobIDs {
		pipe.ZRem(ctx, queueKey, jobID)
		pipe.Del(ctx, kb.Job(jobID))
		pipe.Del(ctx, kb.Lock(jobID))
		pipe.Del(ctx, kb.Logs(jobID))
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}

	return len(jobIDs), nil
}

// RetryJob moves a failed job back to waiting queue
func (q *Queue) RetryJob(ctx context.Context, jobID string) error {
	kb := q.keyBuilder

	// Check if job exists and is in failed queue
	score, err := q.redisClient.ZScore(ctx, kb.Failed(), jobID).Result()
	if err == redis.Nil {
		return fmt.Errorf("job not found in failed queue: %s", jobID)
	}
	if err != nil {
		return err
	}

	// Remove from failed queue
	if err := q.redisClient.ZRem(ctx, kb.Failed(), jobID).Err(); err != nil {
		return err
	}

	// Reset attempts and failure reason
	pipe := q.redisClient.Pipeline()
	pipe.HSet(ctx, kb.Job(jobID), "attemptsMade", 0)
	pipe.HDel(ctx, kb.Job(jobID), "failedReason")

	// Add to wait queue
	pipe.LPush(ctx, kb.Wait(), jobID)

	if _, err := pipe.Exec(ctx); err != nil {
		// Rollback: add back to failed queue
		q.redisClient.ZAdd(ctx, kb.Failed(), redis.Z{Score: score, Member: jobID})
		return err
	}

	return nil
}

// GetJobs retrieves multiple jobs by their IDs
func (q *Queue) GetJobs(ctx context.Context, jobIDs []string) ([]*Job, error) {
	if len(jobIDs) == 0 {
		return []*Job{}, nil
	}

	jobs := make([]*Job, 0, len(jobIDs))

	// Use pipeline for parallel retrieval
	pipe := q.redisClient.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(jobIDs))
	for i, jobID := range jobIDs {
		cmds[i] = pipe.HGetAll(ctx, q.keyBuilder.Job(jobID))
	}

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}

	// Parse results
	for i, cmd := range cmds {
		result, err := cmd.Result()
		if err == redis.Nil || len(result) == 0 {
			continue // Skip non-existent jobs
		}
		if err != nil {
			return nil, err
		}

		job := &Job{ID: jobIDs[i]}
		if name, ok := result["name"]; ok {
			job.Name = name
		}
		if dataJSON, ok := result["data"]; ok {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(dataJSON), &data); err == nil {
				job.Data = data
			}
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// GetWaitingJobs returns jobs waiting in the queue
func (q *Queue) GetWaitingJobs(ctx context.Context, start, end int64) ([]string, error) {
	return q.redisClient.LRange(ctx, q.keyBuilder.Wait(), start, end).Result()
}

// GetActiveJobs returns currently processing jobs
func (q *Queue) GetActiveJobs(ctx context.Context, start, end int64) ([]string, error) {
	return q.redisClient.LRange(ctx, q.keyBuilder.Active(), start, end).Result()
}

// GetCompletedJobs returns completed jobs (most recent first)
func (q *Queue) GetCompletedJobs(ctx context.Context, start, end int64) ([]string, error) {
	return q.redisClient.ZRevRange(ctx, q.keyBuilder.Completed(), start, end).Result()
}

// GetFailedJobs returns failed jobs (most recent first)
func (q *Queue) GetFailedJobs(ctx context.Context, start, end int64) ([]string, error) {
	return q.redisClient.ZRevRange(ctx, q.keyBuilder.Failed(), start, end).Result()
}
