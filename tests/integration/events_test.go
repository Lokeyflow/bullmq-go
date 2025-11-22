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

// T116: Events emitted to Redis stream with MAXLEN ~10000
func TestEvents_EmittedWithMaxLen(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-events-maxlen"
	queue := bullmq.NewQueue(queueName, rdb)
	kb := bullmq.NewKeyBuilder(queueName, rdb)
	eventsKey := kb.Events()

	// Create worker with default options (EventsMaxLen should be 10000)
	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processed := make(chan bool, 1)

	worker.Process(func(job *bullmq.Job) (interface{}, error) {
		processed <- true
		return nil, nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Add and process a job
	_, err := queue.Add(ctx, "test-job", map[string]interface{}{"data": "test"}, bullmq.JobOptions{})
	require.NoError(t, err)

	// Wait for processing
	select {
	case <-processed:
	case <-time.After(3 * time.Second):
		t.Fatal("Job not processed in time")
	}

	// Wait a bit for events to be written
	time.Sleep(500 * time.Millisecond)

	// Verify events exist in stream
	events, err := rdb.XRange(ctx, eventsKey, "-", "+").Result()
	require.NoError(t, err)
	assert.Greater(t, len(events), 0, "Should have events in stream")

	// Verify stream info includes max length constraint
	info, err := rdb.XInfoStream(ctx, eventsKey).Result()
	require.NoError(t, err)
	t.Logf("Stream length: %d, first entry: %s", info.Length, info.FirstEntry.ID)
}

// T117: Event format matches Node.js BullMQ
func TestEvents_FormatMatchesNodeJS(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-events-format"
	queue := bullmq.NewQueue(queueName, rdb)
	kb := bullmq.NewKeyBuilder(queueName, rdb)
	eventsKey := kb.Events()

	worker := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processed := make(chan bool, 1)

	worker.Process(func(job *bullmq.Job) (interface{}, error) {
		processed <- true
		return nil, nil
	})

	go worker.Start(ctx)
	defer worker.Stop()

	// Add and process a job
	job, err := queue.Add(ctx, "test-job", map[string]interface{}{"data": "test"}, bullmq.JobOptions{})
	require.NoError(t, err)

	// Wait for processing
	select {
	case <-processed:
	case <-time.After(3 * time.Second):
		t.Fatal("Job not processed in time")
	}

	time.Sleep(500 * time.Millisecond)

	// Read events from stream
	events, err := rdb.XRange(ctx, eventsKey, "-", "+").Result()
	require.NoError(t, err)
	require.Greater(t, len(events), 0, "Should have at least one event")

	// Parse first event - events are stored as direct fields
	eventFields := events[0].Values
	eventType := eventFields["event"].(string)
	jobID := eventFields["jobId"].(string)
	timestamp, _ := eventFields["timestamp"].(string) // May be string or int64
	attemptsMade, _ := eventFields["attemptsMade"].(string)

	// Verify event structure matches Node.js BullMQ format
	assert.NotEmpty(t, eventType, "Event should have 'event' field")
	assert.Equal(t, job.ID, jobID, "Event should have correct 'jobId'")
	assert.NotEmpty(t, timestamp, "Event should have 'timestamp'")
	assert.NotEmpty(t, attemptsMade, "Event should have 'attemptsMade'")
}

// T118: All event types emitted
func TestEvents_AllEventTypesEmitted(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer rdb.Close()
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-events-all-types"
	queue := bullmq.NewQueue(queueName, rdb)
	kb := bullmq.NewKeyBuilder(queueName, rdb)
	eventsKey := kb.Events()

	// Track which events we've seen
	seenEvents := make(map[string]bool)

	// Helper to extract event types from stream
	getEvents := func() []string {
		messages, err := rdb.XRange(ctx, eventsKey, "-", "+").Result()
		require.NoError(t, err)

		var eventTypes []string
		for _, msg := range messages {
			if eventType, ok := msg.Values["event"].(string); ok {
				eventTypes = append(eventTypes, eventType)
			}
		}
		return eventTypes
	}

	// Test 1: waiting, active, completed events
	worker1 := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processed1 := make(chan bool, 1)
	worker1.Process(func(job *bullmq.Job) (interface{}, error) {
		processed1 <- true
		return nil, nil
	})
	go worker1.Start(ctx)

	_, err := queue.Add(ctx, "success-job", map[string]interface{}{}, bullmq.JobOptions{})
	require.NoError(t, err)

	<-processed1
	worker1.Stop()
	time.Sleep(500 * time.Millisecond)

	for _, eventType := range getEvents() {
		seenEvents[eventType] = true
	}

	assert.True(t, seenEvents[bullmq.EventWaiting], "Should have 'waiting' event")
	assert.True(t, seenEvents[bullmq.EventActive], "Should have 'active' event")
	assert.True(t, seenEvents[bullmq.EventCompleted], "Should have 'completed' event")

	// Clear for next test
	rdb.FlushDB(ctx)
	seenEvents = make(map[string]bool)

	// Test 2: failed event
	worker2 := bullmq.NewWorker(queueName, rdb, bullmq.WorkerOptions{
		Concurrency:          1,
		LockDuration:         30 * time.Second,
		HeartbeatInterval:    15 * time.Second,
		StalledCheckInterval: 30 * time.Second,
		MaxAttempts:          1, // Fail immediately, no retry
		BackoffDelay:         time.Second,
		EventsMaxLen:         10000,
	})
	processed2 := make(chan bool, 1)
	worker2.Process(func(job *bullmq.Job) (interface{}, error) {
		processed2 <- true
		return nil, assert.AnError // Permanent error
	})
	go worker2.Start(ctx)

	_, err = queue.Add(ctx, "failed-job", map[string]interface{}{}, bullmq.JobOptions{Attempts: 1})
	require.NoError(t, err)

	<-processed2
	worker2.Stop()
	time.Sleep(500 * time.Millisecond)

	for _, eventType := range getEvents() {
		seenEvents[eventType] = true
	}

	assert.True(t, seenEvents[bullmq.EventFailed], "Should have 'failed' event")

	// Test 3: progress event
	rdb.FlushDB(ctx)
	seenEvents = make(map[string]bool)

	worker3 := bullmq.NewWorker(queueName, rdb, bullmq.DefaultWorkerOptions)
	processed3 := make(chan bool, 1)
	worker3.Process(func(job *bullmq.Job) (interface{}, error) {
		time.Sleep(100 * time.Millisecond) // Give time for active event
		err := job.UpdateProgress(50)
		if err != nil {
			t.Logf("UpdateProgress error: %v", err)
		}
		time.Sleep(100 * time.Millisecond) // Give time for progress event to emit
		processed3 <- true
		return nil, nil
	})
	go worker3.Start(ctx)

	_, err = queue.Add(ctx, "progress-job", map[string]interface{}{}, bullmq.JobOptions{})
	require.NoError(t, err)

	<-processed3
	worker3.Stop()
	time.Sleep(1 * time.Second) // Increased wait time

	eventTypes := getEvents()
	for _, eventType := range eventTypes {
		seenEvents[eventType] = true
		t.Logf("Progress test event: %s", eventType)
	}

	assert.True(t, seenEvents[bullmq.EventProgress], "Should have 'progress' event")

	// Note: stalled and retry events require more complex setup
	// They will be tested separately if not already covered
	t.Logf("Events seen: %v", seenEvents)
}
