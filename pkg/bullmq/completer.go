package bullmq

import (
	"context"
	"encoding/json"
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
	kb := NewKeyBuilder(c.worker.queueName, c.worker.redisClient)

	// Remove from active
	if err := c.worker.redisClient.LRem(ctx, kb.Active(), 1, job.ID).Err(); err != nil {
		return fmt.Errorf("failed to remove from active: %w", err)
	}

	// Update job data
	job.ReturnValue = returnValue
	job.FinishedOn = time.Now().UnixMilli()

	// Store return value as JSON (BullMQ protocol requirement)
	if returnValue != nil {
		returnValueJSON, err := json.Marshal(returnValue)
		if err != nil {
			// If JSON marshaling fails, store error message
			returnValueJSON = []byte(fmt.Sprintf(`{"error":"failed to marshal return value: %v"}`, err))
		}

		c.worker.redisClient.HSet(ctx, kb.Job(job.ID),
			"returnvalue", string(returnValueJSON),
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

	// Emit completed event with return value
	c.worker.eventEmitter.EmitCompleted(ctx, job, returnValue)

	return nil
}

// Fail marks a job as failed
func (c *Completer) Fail(ctx context.Context, job *Job, err error) error {
	kb := NewKeyBuilder(c.worker.queueName, c.worker.redisClient)

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
	c.worker.eventEmitter.EmitFailed(ctx, job, err)

	return nil
}

