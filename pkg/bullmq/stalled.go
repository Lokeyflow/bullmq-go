package bullmq

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// StalledChecker detects and recovers stalled jobs
type StalledChecker struct {
	worker      *Worker
	stopChan    chan struct{}
	isRunning   atomic.Bool
	checkCount  uint64
	recoveredCount uint64
}

// NewStalledChecker creates a new stalled checker
func NewStalledChecker(worker *Worker) *StalledChecker {
	return &StalledChecker{
		worker:   worker,
		stopChan: make(chan struct{}),
	}
}

// Start begins stalled job checking
func (sc *StalledChecker) Start(ctx context.Context) {
	ticker := time.NewTicker(sc.worker.opts.StalledCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sc.stopChan:
			return
		case <-ticker.C:
			// Skip if previous check still running
			if !sc.isRunning.CompareAndSwap(false, true) {
				continue
			}

			// Run check in background
			go func() {
				defer sc.isRunning.Store(false)
				sc.checkStalledJobs(ctx)
			}()
		}
	}
}

// checkStalledJobs checks for stalled jobs and requeues them
func (sc *StalledChecker) checkStalledJobs(ctx context.Context) {
	sc.checkCount++

	kb := NewKeyBuilder(sc.worker.queueName, sc.worker.redisClient)
	activeKey := kb.Active()

	// Get all active jobs
	activeJobs, err := sc.worker.redisClient.LRange(ctx, activeKey, 0, -1).Result()
	if err != nil {
		return
	}

	// Check each job's lock
	for _, jobID := range activeJobs {
		if sc.isJobStalled(ctx, jobID) {
			sc.recoverStalledJob(ctx, jobID)
		}
	}
}

// isJobStalled checks if a job's lock has expired
func (sc *StalledChecker) isJobStalled(ctx context.Context, jobID string) bool {
	kb := NewKeyBuilder(sc.worker.queueName, sc.worker.redisClient)
	lockKey := kb.Lock(jobID)

	// Check if lock exists
	exists, err := sc.worker.redisClient.Exists(ctx, lockKey).Result()
	if err != nil {
		return false
	}

	// If lock doesn't exist, job is stalled
	return exists == 0
}

// recoverStalledJob moves a stalled job back to wait queue
func (sc *StalledChecker) recoverStalledJob(ctx context.Context, jobID string) {
	kb := NewKeyBuilder(sc.worker.queueName, sc.worker.redisClient)

	// Get job data to check attempts
	jobData, err := sc.worker.redisClient.HGetAll(ctx, kb.Job(jobID)).Result()
	if err != nil {
		return
	}

	// Increment attemptsMade
	var attemptsMade int
	if attemptsStr, ok := jobData["attemptsMade"]; ok {
		// Parse attempts (simplified)
		_ = attemptsStr // TODO: proper parsing
		attemptsMade = 1
	}
	attemptsMade++

	// Remove from active
	sc.worker.redisClient.LRem(ctx, kb.Active(), 1, jobID)

	// Check if max attempts exceeded
	if attemptsMade >= sc.worker.opts.MaxAttempts {
		// Move to failed
		score := float64(time.Now().UnixMilli())
		sc.worker.redisClient.ZAdd(ctx, kb.Failed(), redis.Z{
			Score:  score,
			Member: jobID,
		})
		sc.worker.redisClient.HSet(ctx, kb.Job(jobID), "failedReason", "stalled (max attempts)")
		return
	}

	// Add back to wait queue for retry
	sc.worker.redisClient.LPush(ctx, kb.Wait(), jobID)

	// Update attempts
	sc.worker.redisClient.HSet(ctx, kb.Job(jobID), "attemptsMade", attemptsMade)

	// Emit stalled event
	// TODO: emit event

	sc.recoveredCount++
}

// Stop stops the stalled checker
func (sc *StalledChecker) Stop() {
	close(sc.stopChan)
}

// GetStats returns checker statistics
func (sc *StalledChecker) GetStats() (checkCount, recoveredCount uint64) {
	return sc.checkCount, sc.recoveredCount
}
