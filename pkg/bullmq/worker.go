package bullmq

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// WorkerOptions configures worker behavior
type WorkerOptions struct {
	Concurrency          int
	LockDuration         time.Duration
	HeartbeatInterval    time.Duration
	StalledCheckInterval time.Duration
	MaxAttempts          int
	BackoffDelay         time.Duration
	MaxBackoffDelay      time.Duration
	WorkerID             string
	MaxReconnectAttempts int
	EventsMaxLen         int64
	ShutdownTimeout      time.Duration

	// ResultsQueue enables automatic result forwarding to a dedicated queue
	// for reliable downstream processing (optional, nil = disabled)
	// This is an application-level pattern, not a BullMQ protocol feature.
	ResultsQueue *ResultsQueueConfig
}

// DefaultWorkerOptions provides sensible defaults
var DefaultWorkerOptions = WorkerOptions{
	Concurrency:          1,
	LockDuration:         30 * time.Second,
	HeartbeatInterval:    15 * time.Second,
	StalledCheckInterval: 30 * time.Second,
	MaxAttempts:          3,
	BackoffDelay:         1 * time.Second,
	MaxBackoffDelay:      1 * time.Hour,
	WorkerID:             "",
	MaxReconnectAttempts: 0,
	EventsMaxLen:         10000,
	ShutdownTimeout:      30 * time.Second,
}

// Worker consumes jobs from a queue
type Worker struct {
	queueName        string
	redisClient      redis.Cmdable
	opts             WorkerOptions
	processor        JobProcessor
	heartbeatManager *HeartbeatManager
	stalledChecker   *StalledChecker
	eventEmitter     *EventEmitter
	shutdownChan     chan struct{}
	activeSemaphore  chan struct{}
	wg               sync.WaitGroup
	reconnectAttempts int
	isConnected      bool
	mu               sync.RWMutex

	// Results queue (optional)
	resultsQueue       *Queue
	resultsQueueConfig *ResultsQueueConfig
}

// JobProcessor is the function signature for job processing
// Returns (result, error) matching BullMQ's async processor pattern
type JobProcessor func(*Job) (interface{}, error)

// NewWorker creates a new worker instance
// Accepts both *redis.Client and *redis.ClusterClient via redis.Cmdable interface
func NewWorker(queueName string, redisClient redis.Cmdable, opts WorkerOptions) *Worker {
	// Generate WorkerID if not provided
	if opts.WorkerID == "" {
		opts.WorkerID = generateWorkerID()
	}

	// Set default EventsMaxLen if not specified
	if opts.EventsMaxLen == 0 {
		opts.EventsMaxLen = DefaultWorkerOptions.EventsMaxLen
	}

	worker := &Worker{
		queueName:       queueName,
		redisClient:     redisClient,
		opts:            opts,
		shutdownChan:    make(chan struct{}),
		activeSemaphore: make(chan struct{}, opts.Concurrency),
		isConnected:     true,
	}

	// Initialize event emitter
	worker.eventEmitter = NewEventEmitter(queueName, redisClient, opts.EventsMaxLen)

	// Setup results queue if configured
	if opts.ResultsQueue != nil {
		worker.resultsQueue = NewQueue(opts.ResultsQueue.QueueName, redisClient)
		worker.resultsQueueConfig = opts.ResultsQueue
	}

	return worker
}

// Process registers the job processor function
func (w *Worker) Process(processor JobProcessor) {
	w.processor = processor
}

// ProcessWithResults registers a job processor that automatically forwards
// results to a dedicated results queue for reliable downstream processing.
//
// This is a convenience helper for the results queue pattern recommended by BullMQ:
// https://docs.bullmq.io/guide/returning-job-data
//
// The result is still stored in job.returnvalue for immediate access.
// Results are only sent to the queue on successful job completion.
//
// Example:
//
//	worker.ProcessWithResults("results", func(job *bullmq.Job) (interface{}, error) {
//	    result := processVideo(job.Data)
//	    return result, nil // Automatically sent to "results" queue
//	}, bullmq.ResultsQueueConfig{
//	    OnError: func(jobID string, err error) {
//	        log.Printf("Failed to send result: %v", err)
//	    },
//	})
func (w *Worker) ProcessWithResults(resultsQueueName string, processor JobProcessor, config ...ResultsQueueConfig) {
	// Setup results queue config
	cfg := DefaultResultsQueueConfig
	cfg.QueueName = resultsQueueName

	if len(config) > 0 {
		cfg = config[0]
		cfg.QueueName = resultsQueueName // Ensure queue name is set
	}

	// Initialize results queue
	w.resultsQueue = NewQueue(resultsQueueName, w.redisClient)
	w.resultsQueueConfig = &cfg

	// Register processor
	w.processor = processor
}

// Stop gracefully shuts down the worker
func (w *Worker) Stop() error {
	close(w.shutdownChan)
	w.wg.Wait()
	return nil
}

// GetWorkerID returns the worker's unique identifier
func (w *Worker) GetWorkerID() string {
	return w.opts.WorkerID
}

// generateWorkerID creates a unique worker identifier
// Format: {hostname}-{pid}-{random6}
func generateWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	pid := os.Getpid()
	random := generateRandomHex(6)
	return fmt.Sprintf("%s-%d-%s", hostname, pid, random)
}
