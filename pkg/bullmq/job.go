package bullmq

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Job represents a unit of work in the queue
type Job struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Data          map[string]interface{} `json:"data"`
	Opts          JobOptions             `json:"opts"`
	Progress      int                    `json:"progress"`
	ReturnValue   interface{}            `json:"returnvalue,omitempty"`
	FailedReason  string                 `json:"failedReason,omitempty"`
	StackTrace    []string               `json:"stacktrace,omitempty"`
	Timestamp     int64                  `json:"timestamp"`
	AttemptsMade  int                    `json:"attemptsMade"`
	ProcessedOn   int64                  `json:"processedOn,omitempty"`
	FinishedOn    int64                  `json:"finishedOn,omitempty"`
	WorkerID      string                 `json:"-"` // Not persisted to Redis
	Delay         int64                  `json:"delay"`

	// Internal fields for operations (not serialized)
	queueName   string        `json:"-"`
	redisClient *redis.Client `json:"-"`
	emitter     *EventEmitter `json:"-"`
}

// JobOptions configures job behavior
type JobOptions struct {
	Priority         int           `json:"priority"`
	Delay            time.Duration `json:"delay"`
	Attempts         int           `json:"attempts"`
	Backoff          BackoffConfig `json:"backoff"`
	RemoveOnComplete bool          `json:"removeOnComplete"`
	RemoveOnFail     bool          `json:"removeOnFail"`
}

// BackoffConfig defines retry backoff strategy
type BackoffConfig struct {
	Type  string `json:"type"`  // "fixed" or "exponential"
	Delay int64  `json:"delay"` // Base delay in milliseconds
}

// DefaultJobOptions provides sensible defaults
var DefaultJobOptions = JobOptions{
	Priority:         0,
	Delay:            0,
	Attempts:         3,
	Backoff:          BackoffConfig{Type: "exponential", Delay: 1000},
	RemoveOnComplete: false,
	RemoveOnFail:     false,
}

// UpdateProgress updates job progress (0-100)
func (j *Job) UpdateProgress(progress int) error {
	if progress < 0 || progress > 100 {
		return &ValidationError{Field: "progress", Message: "must be between 0 and 100"}
	}
	j.Progress = progress

	// Update in Redis if client available
	if j.redisClient != nil && j.queueName != "" {
		ctx := context.Background()
		kb := NewKeyBuilder(j.queueName)
		j.redisClient.HSet(ctx, kb.Job(j.ID), "progress", progress)

		// Emit progress event
		if j.emitter != nil {
			j.emitter.EmitProgress(ctx, j, progress)
		}
	}

	return nil
}

// Log appends a log entry to the job
func (j *Job) Log(message string) error {
	if j.redisClient == nil || j.queueName == "" {
		return nil // Silently skip if not connected
	}

	ctx := context.Background()
	kb := NewKeyBuilder(j.queueName)
	logsKey := kb.Logs(j.ID)

	// Append log entry with timestamp
	logEntry := map[string]interface{}{
		"timestamp": time.Now().UnixMilli(),
		"message":   message,
	}

	// Add to list (RPUSH for chronological order)
	return j.redisClient.RPush(ctx, logsKey, logEntry).Err()
}
