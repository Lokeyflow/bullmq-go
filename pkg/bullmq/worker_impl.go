package bullmq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Start begins job consumption from the queue
func (w *Worker) Start(ctx context.Context) error {
	if w.processor == nil {
		return fmt.Errorf("job processor not registered, call Process() first")
	}

	// Validate Redis Cluster compatibility (optional warning)
	w.validateClusterCompatibility()

	// Start background services
	w.startHeartbeatManager(ctx)
	w.startStalledChecker(ctx)

	// Main job consumption loop
	for {
		select {
		case <-ctx.Done():
			return w.gracefulShutdown()
		case <-w.shutdownChan:
			return w.gracefulShutdown()
		default:
			// Check if we have capacity for more jobs
			select {
			case w.activeSemaphore <- struct{}{}:
				// Capacity available, pick up a job
				if err := w.pickupJob(ctx); err != nil {
					// Release semaphore if pickup failed
					<-w.activeSemaphore

					// If no jobs available, wait before retrying
					if err == redis.Nil {
						time.Sleep(100 * time.Millisecond)
						continue
					}

					// Log other errors but continue
					time.Sleep(time.Second)
				}
			default:
				// No capacity, wait
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

// pickupJob attempts to pick up a job from the queue
func (w *Worker) pickupJob(ctx context.Context) error {
	w.mu.RLock()
	if !w.isConnected {
		w.mu.RUnlock()
		return fmt.Errorf("redis disconnected")
	}
	w.mu.RUnlock()

	// Check if queue is paused
	kb := NewKeyBuilder(w.queueName)
	isPaused, err := w.redisClient.HGet(ctx, kb.Meta(), "paused").Result()
	if err != nil && err != redis.Nil {
		return err
	}
	if isPaused == "1" {
		return fmt.Errorf("queue is paused")
	}

	// Try to get job from prioritized queue first, then wait queue
	var jobID string

	// Check prioritized queue (ZSET with scores)
	results, err := w.redisClient.ZPopMin(ctx, kb.Prioritized(), 1).Result()
	if err != nil && err != redis.Nil {
		// Actual error (not just empty queue)
		return err
	}
	if len(results) > 0 {
		jobID = results[0].Member.(string)
	}

	// If no priority jobs, check wait queue (LIST)
	if jobID == "" {
		jobID, err = w.redisClient.RPop(ctx, kb.Wait()).Result()
		if err != nil {
			return err
		}
	}

	// Acquire lock and move to active
	lockToken, err := w.acquireLockAndActivate(ctx, jobID)
	if err != nil {
		return err
	}

	// Process job in goroutine
	w.wg.Add(1)
	go w.processJob(ctx, jobID, lockToken)

	return nil
}

// acquireLockAndActivate acquires a lock and moves job to active
func (w *Worker) acquireLockAndActivate(ctx context.Context, jobID string) (LockToken, error) {
	kb := NewKeyBuilder(w.queueName)
	lockToken := NewLockToken()

	// Set lock with TTL
	lockKey := kb.Lock(jobID)
	err := w.redisClient.SetEx(ctx, lockKey, lockToken.String(), w.opts.LockDuration).Err()
	if err != nil {
		return lockToken, fmt.Errorf("failed to acquire lock: %w", err)
	}

	// Add to active list
	activeKey := kb.Active()
	if err := w.redisClient.LPush(ctx, activeKey, jobID).Err(); err != nil {
		// Release lock if active push fails
		w.redisClient.Del(ctx, lockKey)
		return lockToken, fmt.Errorf("failed to move to active: %w", err)
	}

	return lockToken, nil
}

// processJob processes a single job
func (w *Worker) processJob(ctx context.Context, jobID string, lockToken LockToken) {
	defer w.wg.Done()
	defer func() { <-w.activeSemaphore }() // Release semaphore

	// Get job data from Redis
	job, err := w.getJobData(ctx, jobID)
	if err != nil {
		return
	}

	// Set WorkerID
	job.WorkerID = w.opts.WorkerID

	// Emit active event
	w.eventEmitter.EmitActive(ctx, job)

	// Start heartbeat for this job
	if w.heartbeatManager != nil {
		w.heartbeatManager.StartHeartbeat(ctx, jobID, lockToken)
		defer w.heartbeatManager.StopHeartbeat(jobID)
	}

	// Execute job processor
	err = w.processor(job)

	// Handle result
	if err != nil {
		w.handleJobFailure(ctx, job, err)
	} else {
		w.handleJobSuccess(ctx, job)
	}
}

// getJobData retrieves job data from Redis
func (w *Worker) getJobData(ctx context.Context, jobID string) (*Job, error) {
	kb := NewKeyBuilder(w.queueName)
	jobKey := kb.Job(jobID)

	// Get job hash from Redis
	data, err := w.redisClient.HGetAll(ctx, jobKey).Result()
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	// Parse job data
	job := &Job{
		ID:          jobID,
		Data:        make(map[string]interface{}),
		queueName:   w.queueName,
		redisClient: w.redisClient,
		emitter:     w.eventEmitter,
	}

	// Parse string fields
	if name, ok := data["name"]; ok {
		job.Name = name
	}

	// Parse JSON data field
	if dataJSON, ok := data["data"]; ok && dataJSON != "" {
		if err := json.Unmarshal([]byte(dataJSON), &job.Data); err != nil {
			return nil, fmt.Errorf("failed to unmarshal job data: %w", err)
		}
	}

	// Parse JSON opts field
	if optsJSON, ok := data["opts"]; ok && optsJSON != "" {
		if err := json.Unmarshal([]byte(optsJSON), &job.Opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal job opts: %w", err)
		}
	}

	// Parse numeric fields
	if progress, ok := data["progress"]; ok {
		fmt.Sscanf(progress, "%d", &job.Progress)
	}
	if timestamp, ok := data["timestamp"]; ok {
		fmt.Sscanf(timestamp, "%d", &job.Timestamp)
	}
	if attemptsMade, ok := data["attemptsMade"]; ok {
		fmt.Sscanf(attemptsMade, "%d", &job.AttemptsMade)
	}
	if delay, ok := data["delay"]; ok {
		fmt.Sscanf(delay, "%d", &job.Delay)
	}

	// Parse optional fields
	if failedReason, ok := data["failedReason"]; ok {
		job.FailedReason = failedReason
	}
	if processedOn, ok := data["processedOn"]; ok {
		fmt.Sscanf(processedOn, "%d", &job.ProcessedOn)
	}
	if finishedOn, ok := data["finishedOn"]; ok {
		fmt.Sscanf(finishedOn, "%d", &job.FinishedOn)
	}

	return job, nil
}

// handleJobSuccess handles successful job completion
func (w *Worker) handleJobSuccess(ctx context.Context, job *Job) {
	kb := NewKeyBuilder(w.queueName)

	// Remove from active
	w.redisClient.LRem(ctx, kb.Active(), 1, job.ID)

	// Remove lock
	w.redisClient.Del(ctx, kb.Lock(job.ID))

	// Add to completed (if not removeOnComplete)
	if !job.Opts.RemoveOnComplete {
		score := float64(time.Now().UnixMilli())
		w.redisClient.ZAdd(ctx, kb.Completed(), redis.Z{
			Score:  score,
			Member: job.ID,
		})
	} else {
		// Remove job data
		w.redisClient.Del(ctx, kb.Job(job.ID))
	}

	// Emit completed event
	w.eventEmitter.EmitCompleted(ctx, job, nil)
}

// handleJobFailure handles job failure
func (w *Worker) handleJobFailure(ctx context.Context, job *Job, err error) {
	// Categorize error
	category := CategorizeError(err)

	// If permanent error or max attempts reached, move to failed
	if category == ErrorCategoryPermanent || job.AttemptsMade >= job.Opts.Attempts {
		w.moveToFailed(ctx, job, err)
		return
	}

	// Otherwise, retry with backoff
	w.retryJob(ctx, job, err)
}

// moveToFailed moves job to failed queue
func (w *Worker) moveToFailed(ctx context.Context, job *Job, err error) {
	kb := NewKeyBuilder(w.queueName)

	// Remove from active
	w.redisClient.LRem(ctx, kb.Active(), 1, job.ID)

	// Remove lock
	w.redisClient.Del(ctx, kb.Lock(job.ID))

	// Add to failed (if not removeOnFail)
	if !job.Opts.RemoveOnFail {
		score := float64(time.Now().UnixMilli())
		w.redisClient.ZAdd(ctx, kb.Failed(), redis.Z{
			Score:  score,
			Member: job.ID,
		})

		// Store failure reason
		w.redisClient.HSet(ctx, kb.Job(job.ID), "failedReason", err.Error())
	} else {
		// Remove job data
		w.redisClient.Del(ctx, kb.Job(job.ID))
	}

	// Emit failed event
	w.eventEmitter.EmitFailed(ctx, job, err)
}

// retryJob retries a failed job with backoff
func (w *Worker) retryJob(ctx context.Context, job *Job, err error) {
	kb := NewKeyBuilder(w.queueName)

	// Increment attempts
	job.AttemptsMade++

	// Calculate backoff delay
	delay := CalculateBackoff(job.AttemptsMade, w.opts.BackoffDelay, w.opts.MaxBackoffDelay)

	// Remove from active
	w.redisClient.LRem(ctx, kb.Active(), 1, job.ID)

	// Add to delayed queue with backoff
	retryTimestamp := time.Now().Add(delay).UnixMilli()
	w.redisClient.ZAdd(ctx, kb.Delayed(), redis.Z{
		Score:  float64(retryTimestamp),
		Member: job.ID,
	})

	// Update attempts in Redis
	w.redisClient.HSet(ctx, kb.Job(job.ID), "attemptsMade", job.AttemptsMade)
}

// extendLockPeriodically extends job lock via heartbeat
func (w *Worker) extendLockPeriodically(ctx context.Context, jobID string) {
	ticker := time.NewTicker(w.opts.HeartbeatInterval)
	defer ticker.Stop()

	kb := NewKeyBuilder(w.queueName)
	lockKey := kb.Lock(jobID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Extend lock TTL
			err := w.redisClient.Expire(ctx, lockKey, w.opts.LockDuration).Err()
			if err != nil {
				// Log heartbeat failure but continue processing
				// Worker will be recovered by stalled checker if lock expires
			}
		}
	}
}

// startHeartbeatManager starts the heartbeat manager
func (w *Worker) startHeartbeatManager(ctx context.Context) {
	w.heartbeatManager = NewHeartbeatManager(w)
}

// startStalledChecker starts the stalled job checker
func (w *Worker) startStalledChecker(ctx context.Context) {
	w.stalledChecker = NewStalledChecker(w)
	go w.stalledChecker.Start(ctx)
}

// gracefulShutdown waits for active jobs to complete
func (w *Worker) gracefulShutdown() error {
	// Wait for all jobs to finish with timeout
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(w.opts.ShutdownTimeout):
		return fmt.Errorf("shutdown timeout exceeded")
	}
}

// validateClusterCompatibility validates that all keys use proper hash tags for Redis Cluster
// This is an optional warning - the worker will function in both single-instance and cluster modes
func (w *Worker) validateClusterCompatibility() {
	// Generate sample keys for validation
	kb := NewKeyBuilder(w.queueName)
	keys := []string{
		kb.Wait(),
		kb.Active(),
		kb.Prioritized(),
		kb.Meta(),
		kb.Job("sample-id"),
		kb.Lock("sample-id"),
	}

	// Validate all keys hash to same slot
	allSame, slot, _ := ValidateHashTags(keys)
	if !allSame {
		// This should never happen if KeyBuilder is implemented correctly
		fmt.Printf("⚠️  WARNING: Queue '%s' keys do NOT all hash to the same Redis Cluster slot. "+
			"Multi-key Lua scripts may fail with CROSSSLOT errors in cluster mode.\n", w.queueName)
		return
	}

	// Check if we're connected to a cluster
	isCluster := IsRedisCluster(w.redisClient)
	if isCluster {
		fmt.Printf("✅ Redis Cluster detected: Queue '%s' keys validated (slot %d)\n", w.queueName, slot)
	}
	// If not cluster, no need to log anything (most common case)
}

// Helper to convert WorkerOptions to JobOptions
func (opts WorkerOptions) toJobOptions() JobOptions {
	return JobOptions{
		Attempts: opts.MaxAttempts,
		Backoff: BackoffConfig{
			Type:  "exponential",
			Delay: opts.BackoffDelay.Milliseconds(),
		},
	}
}
