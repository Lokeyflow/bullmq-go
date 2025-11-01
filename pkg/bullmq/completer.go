package bullmq

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Completer handles job completion and failure
type Completer struct {
	worker *Worker
}

// NewCompleter creates a new completer
func NewCompleter(worker *Worker) *Completer {
	return &Completer{worker: worker}
}

// Complete marks a job as completed
func (c *Completer) Complete(ctx context.Context, job *Job, returnValue interface{}) error {
	kb := NewKeyBuilder(c.worker.queueName)

	// Remove from active
	if err := c.worker.redisClient.LRem(ctx, kb.Active(), 1, job.ID).Err(); err != nil {
		return fmt.Errorf("failed to remove from active: %w", err)
	}

	// Update job data
	job.ReturnValue = returnValue
	job.FinishedOn = time.Now().UnixMilli()

	// Store return value
	if returnValue != nil {
		c.worker.redisClient.HSet(ctx, kb.Job(job.ID),
			"returnValue", fmt.Sprintf("%v", returnValue),
			"finishedOn", job.FinishedOn,
		)
	}

	// Handle removeOnComplete
	if job.Opts.RemoveOnComplete {
		// Remove job completely
		c.worker.redisClient.Del(ctx, kb.Job(job.ID))
		c.worker.redisClient.Del(ctx, kb.Lock(job.ID))
		c.worker.redisClient.Del(ctx, kb.Logs(job.ID))
	} else {
		// Add to completed set
		score := float64(job.FinishedOn)
		c.worker.redisClient.ZAdd(ctx, kb.Completed(), redis.Z{
			Score:  score,
			Member: job.ID,
		})
	}

	// Release lock
	c.worker.redisClient.Del(ctx, kb.Lock(job.ID))

	// Emit completed event
	c.emitCompletedEvent(ctx, job)

	return nil
}

// Fail marks a job as failed
func (c *Completer) Fail(ctx context.Context, job *Job, err error) error {
	kb := NewKeyBuilder(c.worker.queueName)

	// Remove from active
	c.worker.redisClient.LRem(ctx, kb.Active(), 1, job.ID)

	// Update job data
	job.FailedReason = err.Error()
	job.FinishedOn = time.Now().UnixMilli()

	// Store failure info
	c.worker.redisClient.HSet(ctx, kb.Job(job.ID),
		"failedReason", job.FailedReason,
		"finishedOn", job.FinishedOn,
	)

	// Handle removeOnFail
	if job.Opts.RemoveOnFail {
		// Remove job completely
		c.worker.redisClient.Del(ctx, kb.Job(job.ID))
		c.worker.redisClient.Del(ctx, kb.Lock(job.ID))
		c.worker.redisClient.Del(ctx, kb.Logs(job.ID))
	} else {
		// Add to failed set
		score := float64(job.FinishedOn)
		c.worker.redisClient.ZAdd(ctx, kb.Failed(), redis.Z{
			Score:  score,
			Member: job.ID,
		})
	}

	// Release lock
	c.worker.redisClient.Del(ctx, kb.Lock(job.ID))

	// Emit failed event
	c.emitFailedEvent(ctx, job)

	return nil
}

// emitCompletedEvent emits a completed event
func (c *Completer) emitCompletedEvent(ctx context.Context, job *Job) {
	kb := NewKeyBuilder(c.worker.queueName)

	event := map[string]interface{}{
		"event":        EventCompleted,
		"jobId":        job.ID,
		"returnValue":  job.ReturnValue,
		"timestamp":    time.Now().UnixMilli(),
		"attemptsMade": job.AttemptsMade,
	}

	c.worker.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: kb.Events(),
		MaxLen: 10000,
		Approx: true,
		Values: event,
	})
}

// emitFailedEvent emits a failed event
func (c *Completer) emitFailedEvent(ctx context.Context, job *Job) {
	kb := NewKeyBuilder(c.worker.queueName)

	event := map[string]interface{}{
		"event":        EventFailed,
		"jobId":        job.ID,
		"failedReason": job.FailedReason,
		"timestamp":    time.Now().UnixMilli(),
		"attemptsMade": job.AttemptsMade,
	}

	c.worker.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: kb.Events(),
		MaxLen: 10000,
		Approx: true,
		Values: event,
	})
}
