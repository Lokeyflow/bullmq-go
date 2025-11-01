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

// UpdateProgress atomically updates job progress (0-100) using Lua script
// The Lua script ensures atomicity and automatically emits a progress event
func (j *Job) UpdateProgress(progress int) error {
	if progress < 0 || progress > 100 {
		return &ValidationError{Field: "progress", Message: "must be between 0 and 100"}
	}

	// Update local state
	j.Progress = progress

	// Update in Redis atomically if client available
	if j.redisClient != nil && j.queueName != "" {
		ctx := context.Background()
		updater := NewProgressUpdater(j.queueName, j.redisClient)

		_, err := updater.UpdateProgress(ctx, j.ID, progress)
		if err != nil {
			return err
		}
	}

	return nil
}

// Log atomically appends a log entry to the job using Lua script
// The Lua script automatically trims logs to max 1000 entries (LIFO)
func (j *Job) Log(message string) error {
	if j.redisClient == nil || j.queueName == "" {
		return nil // Silently skip if not connected
	}

	ctx := context.Background()
	logManager := NewLogManager(j.queueName, j.redisClient, DefaultMaxLogs)

	_, err := logManager.AddLog(ctx, j.ID, message)
	return err
}
