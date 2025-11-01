package bullmq

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// Event represents a job lifecycle event
type Event struct {
	EventType    string                 `json:"event"`
	JobID        string                 `json:"jobId"`
	Timestamp    int64                  `json:"timestamp"`
	AttemptsMade int                    `json:"attemptsMade"`
	Data         map[string]interface{} `json:"data"`
}

// Event types
const (
	EventWaiting   = "waiting"
	EventActive    = "active"
	EventProgress  = "progress"
	EventCompleted = "completed"
	EventFailed    = "failed"
	EventStalled   = "stalled"
	EventRetry     = "retry"
)

// EventEmitter publishes events to Redis streams
type EventEmitter struct {
	queueName   string
	redisClient *redis.Client
	maxLen      int64
}

// NewEventEmitter creates a new event emitter
func NewEventEmitter(queueName string, redisClient *redis.Client, maxLen int64) *EventEmitter {
	return &EventEmitter{
		queueName:   queueName,
		redisClient: redisClient,
		maxLen:      maxLen,
	}
}

// Emit publishes an event to the Redis stream
func (ee *EventEmitter) Emit(ctx context.Context, event Event) error {
	kb := NewKeyBuilder(ee.queueName)
	streamKey := kb.Events()

	// Convert event to map for XADD
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}

	// Publish to stream with MAXLEN
	_, err = ee.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		MaxLen: ee.maxLen,
		Approx: true, // Use approximate trimming for performance
		Values: map[string]interface{}{
			"event": string(eventJSON),
		},
	}).Result()

	return err
}

// EmitWaiting emits a waiting event
func (ee *EventEmitter) EmitWaiting(ctx context.Context, job *Job) error {
	return ee.Emit(ctx, Event{
		EventType:    EventWaiting,
		JobID:        job.ID,
		Timestamp:    time.Now().UnixMilli(),
		AttemptsMade: job.AttemptsMade,
	})
}

// EmitActive emits an active event
func (ee *EventEmitter) EmitActive(ctx context.Context, job *Job) error {
	return ee.Emit(ctx, Event{
		EventType:    EventActive,
		JobID:        job.ID,
		Timestamp:    time.Now().UnixMilli(),
		AttemptsMade: job.AttemptsMade,
	})
}

// EmitCompleted emits a completed event
func (ee *EventEmitter) EmitCompleted(ctx context.Context, job *Job, returnValue interface{}) error {
	return ee.Emit(ctx, Event{
		EventType:    EventCompleted,
		JobID:        job.ID,
		Timestamp:    time.Now().UnixMilli(),
		AttemptsMade: job.AttemptsMade,
		Data: map[string]interface{}{
			"returnValue": returnValue,
		},
	})
}

// EmitFailed emits a failed event
func (ee *EventEmitter) EmitFailed(ctx context.Context, job *Job, err error) error {
	return ee.Emit(ctx, Event{
		EventType:    EventFailed,
		JobID:        job.ID,
		Timestamp:    time.Now().UnixMilli(),
		AttemptsMade: job.AttemptsMade,
		Data: map[string]interface{}{
			"error": err.Error(),
		},
	})
}

// EmitProgress emits a progress event
func (ee *EventEmitter) EmitProgress(ctx context.Context, job *Job, progress int) error {
	return ee.Emit(ctx, Event{
		EventType:    EventProgress,
		JobID:        job.ID,
		Timestamp:    time.Now().UnixMilli(),
		AttemptsMade: job.AttemptsMade,
		Data: map[string]interface{}{
			"progress": progress,
		},
	})
}
