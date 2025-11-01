# Data Model: BullMQ Go Client Library

**Feature ID**: 001-bullmq-protocol-implementation
**Date**: 2025-10-30
**Status**: Draft

---

## Overview

This document defines all data structures and entities for the BullMQ Go client library. These structures provide the foundation for job queue operations, state management, and interoperability with Node.js BullMQ.

---

## Entity Relationship Diagram

```
┌─────────────────┐
│      Job        │
├─────────────────┤
│ ID              │◄──┐
│ Name            │   │
│ Data            │   │
│ Opts            │───┼──► JobOptions ───► BackoffConfig
│ Progress        │   │
│ Delay           │   │
│ Timestamp       │   │
│ AttemptsMade    │   │
│ ProcessedOn     │   │
│ FinishedOn      │   │
│ WorkerID        │   │
│ ReturnValue     │   │
│ FailedReason    │   │
│ Stacktrace      │   │
│ LockToken       │───┼──► LockToken (UUID v4)
└─────────────────┘   │
        │             │
        │ 0..*        │
        │             │
        ▼             │
┌─────────────────┐   │
│     Event       │   │
├─────────────────┤   │
│ EventType       │   │
│ JobID           │───┘
│ Timestamp       │
│ AttemptsMade    │
│ Data            │
└─────────────────┘

┌─────────────────┐
│     Worker      │
├─────────────────┤
│ queueName       │◄───┐
│ redisClient     │    │
│ opts            │────► WorkerOptions
│ processor       │
│ heartbeat       │
│ stalledChecker  │
│ eventEmitter    │
└─────────────────┘

┌─────────────────┐
│     Queue       │
├─────────────────┤
│ name            │
│ redisClient     │
│ keyBuilder      │────► KeyBuilder (Redis keys with hash tags)
│ scripts         │
└─────────────────┘
```

---

## 1. Job

**Description**: Core entity representing a unit of work in the queue. Jobs contain user-defined data, configuration options, state information, and processing metadata.

**Go Structure**:
```go
type Job struct {
    ID            string                 `json:"id"`
    Name          string                 `json:"name"`
    Data          map[string]interface{} `json:"data"`
    Opts          JobOptions             `json:"opts"`
    Progress      int                    `json:"progress"`
    ReturnValue   interface{}            `json:"returnvalue"`
    FailedReason  string                 `json:"failedReason"`
    StackTrace    []string               `json:"stacktrace"`
    Timestamp     int64                  `json:"timestamp"`
    AttemptsMade  int                    `json:"attemptsMade"`
    ProcessedOn   int64                  `json:"processedOn"`
    FinishedOn    int64                  `json:"finishedOn"`
    WorkerID      string                 `json:"-"`
}
```

**Fields**:
- `ID`: Unique identifier (auto-generated UUID or user-provided). Used for Redis key generation and idempotency tracking. Max 255 characters.
- `Name`: Job type identifier for processor routing (e.g., "send-email", "process-payment"). Max 255 characters.
- `Data`: User-defined job payload. Must be JSON-serializable. Maximum size 10MB after JSON serialization.
- `Opts`: Job configuration options (priority, delay, retry settings). See JobOptions structure.
- `Progress`: Current progress percentage (0-100). Updated via `job.UpdateProgress()` API. Default: 0.
- `ReturnValue`: Result data returned by successful job processing. Stored in Redis on completion.
- `FailedReason`: Human-readable error message on job failure. Stored in Redis on failure.
- `StackTrace`: Error stack trace for debugging failed jobs. Array of stack frame strings.
- `Timestamp`: Job creation timestamp in Unix milliseconds. Set automatically on job creation.
- `AttemptsMade`: Number of processing attempts (increments on retry). Used for exponential backoff calculation.
- `ProcessedOn`: Timestamp when job was last picked up for processing (Unix ms). Updated on each attempt.
- `FinishedOn`: Timestamp when job completed or failed (Unix ms). Terminal state timestamp.
- `WorkerID`: Identifier of the worker currently processing this job. Not persisted to Redis (runtime-only).

**Validation Rules**:
- `ID` MUST be non-empty string (max 255 characters)
- `Name` MUST be non-empty string (max 255 characters)
- `Data` MUST serialize to valid JSON
- `Data` + `Opts` combined size MUST be <= 10MB after JSON serialization
- `Progress` MUST be integer in range [0, 100]
- `Timestamp` MUST be positive integer (Unix milliseconds)
- `AttemptsMade` MUST be non-negative integer

**State Transitions**:
```
┌──────────────┐
│   Created    │ Data filled, stored in Redis hash
└──────┬───────┘
       │ Added to wait/prioritized queue
       ▼
┌──────────────┐
│   Waiting    │ In bull:{queue}:wait or :prioritized
└──────┬───────┘
       │ moveToActive.lua
       ▼
┌──────────────┐
│    Active    │ In :active, lock acquired, heartbeat running
└──────┬───────┘
       │ Processing
       ▼
    ┌──┴────────────────────┬───────────────┐
    ▼                       ▼               ▼
┌──────────┐         ┌──────────┐    ┌──────────┐
│Completed │         │  Failed  │    │ Stalled  │
└──────────┘         └──────┬───┘    └────┬─────┘
  :completed           :failed │           │
  returnvalue    attemptsMade < max?   attemptsMade++
                       ▼        │           │
                    ┌──────────┐│           │
                    │   DLQ    ││           │
                    └──────────┘│           │
                       │        │           │
                       └────────┴───────────┘
                             Retry → back to Waiting
```

**Redis Storage**:

Hash Key: `bull:{queue}:{jobId}`

Hash Fields:
```
name: "send-email"
data: "{\"to\":\"user@example.com\",\"subject\":\"Welcome\",\"body\":\"...\"}"
opts: "{\"priority\":1,\"attempts\":3,\"backoff\":{\"type\":\"exponential\",\"delay\":1000}}"
progress: "0"
delay: "0"
timestamp: "1698765432000"
attemptsMade: "0"
processedOn: "1698765433000"
finishedOn: "0"
workerId: "worker-1"
returnvalue: "{\"messageId\":\"abc123\"}" (on success)
failedReason: "SMTP connection failed" (on failure)
stacktrace: "[\"at sendEmail\",\"at worker.go:123\"]"
```

**JSON Format**:
```json
{
  "id": "job-123",
  "name": "send-email",
  "data": {
    "to": "user@example.com",
    "subject": "Welcome"
  },
  "opts": {
    "priority": 10,
    "delay": 0,
    "attempts": 3,
    "backoff": {
      "type": "exponential",
      "delay": 1000
    },
    "removeOnComplete": true,
    "removeOnFail": false
  },
  "progress": 50,
  "timestamp": 1698765432000,
  "attemptsMade": 1,
  "processedOn": 1698765435000
}
```

---

## 2. JobOptions

**Description**: Configuration options for job behavior, including priority, scheduling, retry settings, and cleanup policies.

**Go Structure**:
```go
type JobOptions struct {
    Priority         int           `json:"priority"`
    Delay            int64         `json:"delay"`
    Attempts         int           `json:"attempts"`
    Backoff          BackoffConfig `json:"backoff"`
    RemoveOnComplete bool          `json:"removeOnComplete"`
    RemoveOnFail     bool          `json:"removeOnFail"`
}
```

**Fields**:
- `Priority`: Job priority for queue ordering. Higher values processed first. Range: 0-2147483647. Default: 0.
- `Delay`: Milliseconds to wait before job becomes processable. Jobs with delay > 0 go to delayed queue. Default: 0.
- `Attempts`: Maximum number of processing attempts before job moves to failed queue (DLQ). Default: 3.
- `Backoff`: Retry backoff configuration (type and base delay). See BackoffConfig structure.
- `RemoveOnComplete`: If true, job is removed from Redis on successful completion. Default: false.
- `RemoveOnFail`: If true, job is removed from Redis on final failure. Default: false.

**Validation Rules**:
- `Priority` MUST be >= 0 (negative values rejected)
- `Delay` MUST be >= 0 (negative values rejected)
- `Attempts` MUST be > 0 (zero or negative values rejected)
- `Backoff.Delay` MUST be > 0 if backoff is specified
- `Backoff.Type` MUST be "fixed" or "exponential" (other values rejected)

**Defaults**:
```go
var DefaultJobOptions = JobOptions{
    Priority:         0,
    Delay:            0,
    Attempts:         3,
    Backoff: BackoffConfig{
        Type:  "exponential",
        Delay: 1000,
    },
    RemoveOnComplete: false,
    RemoveOnFail:     false,
}
```

**Semantics**:
- **Priority 0**: Job added to `bull:{queue}:wait` (LIST, FIFO)
- **Priority > 0**: Job added to `bull:{queue}:prioritized` (ZSET, score = priority)
- **Delay > 0**: Job added to `bull:{queue}:delayed` (ZSET, score = timestamp + delay)

**JSON Format**:
```json
{
  "priority": 10,
  "delay": 5000,
  "attempts": 3,
  "backoff": {
    "type": "exponential",
    "delay": 1000
  },
  "removeOnComplete": true,
  "removeOnFail": false
}
```

---

## 3. BackoffConfig

**Description**: Configuration for retry backoff strategy. Determines delay duration between retry attempts.

**Go Structure**:
```go
type BackoffConfig struct {
    Type  string `json:"type"`
    Delay int64  `json:"delay"`
}
```

**Fields**:
- `Type`: Backoff strategy type. Valid values: "fixed", "exponential". Default: "exponential".
  - **fixed**: Constant delay between retries (delay remains same for all attempts)
  - **exponential**: Exponentially increasing delay (delay doubles on each attempt)
- `Delay`: Base delay in milliseconds. Used as constant delay (fixed) or initial delay (exponential). Minimum: 1ms.

**Validation Rules**:
- `Type` MUST be "fixed" or "exponential" (case-sensitive, other values rejected)
- `Delay` MUST be > 0 (zero or negative values rejected)
- `Delay` SHOULD be reasonable (warn if > 3600000ms / 1 hour)

**Backoff Calculation**:

**Fixed Backoff**:
```
delayMs = BackoffConfig.Delay
```

**Exponential Backoff**:
```
delayMs = min(BackoffConfig.Delay * 2^(attemptsMade-1), maxBackoffDelay)
maxBackoffDelay = 3600000 (1 hour, configurable)
```

Example with Delay = 1000ms:
- Attempt 1: 1000ms (1s)
- Attempt 2: 2000ms (2s)
- Attempt 3: 4000ms (4s)
- Attempt 10: 512000ms (8.5 minutes)
- Attempt 11+: 3600000ms (1 hour, capped)

**JSON Format**:
```json
{
  "type": "exponential",
  "delay": 1000
}
```

---

## 4. Worker

**Description**: Job consumer that picks up jobs from queue, processes them, manages locks, and handles failures. The worker manages concurrency, heartbeat, stalled detection, and graceful shutdown.

**Go Structure**:
```go
type Worker struct {
    queueName            string
    redisClient          *redis.Client
    opts                 WorkerOptions
    processor            JobProcessor
    heartbeatManager     *HeartbeatManager
    stalledChecker       *StalledChecker
    eventEmitter         *EventEmitter
    shutdownChan         chan struct{}
    activeSemaphore      chan struct{}
    wg                   sync.WaitGroup
    reconnectAttempts    int
    isConnected          bool
}

type JobProcessor func(*Job) error
```

**Fields**:
- `queueName`: Name of the queue to consume jobs from. Must match producer queue name.
- `redisClient`: Redis client for queue operations. Must be configured for cluster compatibility (hash tags).
- `opts`: Worker configuration (concurrency, timeouts, retry settings). See WorkerOptions structure.
- `processor`: User-defined function for job processing. Must be idempotent (at-least-once delivery).
- `heartbeatManager`: Manages periodic lock extensions (every 15s) to prevent stalled detection during long jobs.
- `stalledChecker`: Background process that detects and requeues jobs with expired locks (every 30s).
- `eventEmitter`: Publishes job lifecycle events to Redis stream (waiting, active, completed, failed, etc.).
- `shutdownChan`: Channel for graceful shutdown signaling. Closed on shutdown to stop all goroutines.
- `activeSemaphore`: Buffered channel limiting concurrent job processing (size = opts.Concurrency).
- `wg`: Wait group tracking active jobs for graceful shutdown (waits for all jobs to complete).
- `reconnectAttempts`: Counter for Redis reconnection attempts (increments on disconnect).
- `isConnected`: Current Redis connection status (used to pause job pickup during reconnection).

**State Management**:
- **Stopped**: Initial state, not consuming jobs
- **Running**: Actively picking up and processing jobs
- **Disconnected**: Redis connection lost, attempting reconnection (no new jobs picked up)
- **ShuttingDown**: Graceful shutdown in progress, waiting for active jobs to complete
- **Stopped**: Shutdown complete, all jobs finished or requeued

**Lifecycle**:
1. **Initialization**: `NewWorker()` creates worker with configuration
2. **Start**: `worker.Start(ctx)` begins job consumption loop
3. **Processing**: Worker picks up jobs, acquires locks, invokes processor function
4. **Heartbeat**: Lock extended every 15 seconds to prevent stalled detection
5. **Completion**: Job moved to completed/failed queue based on processor result
6. **Shutdown**: `worker.Stop()` triggers graceful shutdown (waits for active jobs)

**Concurrency Model**:
```
┌─────────────────────────────────────────────┐
│ Worker (queueName: "myqueue")               │
│                                             │
│  Main Loop:                                 │
│  ┌─────────────────────────────────────┐   │
│  │ 1. Wait for semaphore slot          │   │
│  │    (blocks if Concurrency jobs      │   │
│  │     already active)                 │   │
│  │                                     │   │
│  │ 2. Pick job from Redis              │   │
│  │    (moveToActive.lua)               │   │
│  │                                     │   │
│  │ 3. Spawn goroutine:                 │   │
│  │    ┌─────────────────────────────┐  │   │
│  │    │ a. Start heartbeat          │  │   │
│  │    │ b. Process job              │  │   │
│  │    │ c. Stop heartbeat           │  │   │
│  │    │ d. Complete/fail job        │  │   │
│  │    │ e. Release semaphore slot   │  │   │
│  │    └─────────────────────────────┘  │   │
│  │                                     │   │
│  │ 4. Repeat until shutdown            │   │
│  └─────────────────────────────────────┘   │
│                                             │
│  Background:                                │
│  - Heartbeat Manager (goroutine per job)   │
│  - Stalled Checker (every 30s)             │
│  - Reconnection Handler (on disconnect)    │
└─────────────────────────────────────────────┘
```

---

## 5. WorkerOptions

**Description**: Configuration options for worker behavior, including concurrency, timeouts, retry settings, and observability.

**Go Structure**:
```go
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
}
```

**Fields**:
- `Concurrency`: Maximum number of jobs processed simultaneously. Each job runs in separate goroutine. Default: 1.
- `LockDuration`: Time-to-live for job locks in Redis. Balance recovery speed vs network tolerance. Default: 30s.
- `HeartbeatInterval`: Frequency of lock extensions during job processing. Should be 50% of LockDuration. Default: 15s.
- `StalledCheckInterval`: Frequency of stalled job detection cycles. Determines max recovery time (2x interval). Default: 30s.
- `MaxAttempts`: Maximum retry attempts before job moves to failed queue (DLQ). Overrides JobOptions.Attempts if set. Default: 3.
- `BackoffDelay`: Base delay for exponential backoff calculation. First retry delay. Default: 1s.
- `MaxBackoffDelay`: Maximum delay cap for exponential backoff. Prevents unbounded growth. Default: 1h (3600000ms).
- `WorkerID`: Unique worker identifier for logs/metrics. Auto-generated as `{hostname}-{pid}-{random6}` if empty.
- `MaxReconnectAttempts`: Maximum Redis reconnection attempts before worker fails. 0 = unlimited (default).
- `EventsMaxLen`: Maximum events retained in Redis stream. Uses approximate trimming (~) for performance. Default: 10000.
- `ShutdownTimeout`: Maximum time to wait for active jobs during graceful shutdown. After timeout, jobs are requeued. Default: 30s.

**Validation Rules**:
- `Concurrency` MUST be > 0 (minimum 1 concurrent job)
- `LockDuration` MUST be > 0 (minimum 1 second recommended)
- `HeartbeatInterval` SHOULD be <= 50% of `LockDuration` (prevent lock expiration)
- `StalledCheckInterval` MUST be > 0 (minimum 1 second)
- `MaxAttempts` MUST be > 0 (minimum 1 attempt)
- `BackoffDelay` MUST be >= 0 (0 = no delay)
- `MaxBackoffDelay` MUST be >= `BackoffDelay`
- `WorkerID` SHOULD be <= 255 characters (Redis key length limit)
- `MaxReconnectAttempts` MUST be >= 0 (0 = unlimited)
- `EventsMaxLen` MUST be > 0 (minimum 100 recommended)
- `ShutdownTimeout` MUST be >= 0 (0 = immediate shutdown, no wait)

**Defaults**:
```go
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
```

**WorkerID Generation**:
```go
func generateWorkerID() string {
    hostname, _ := os.Hostname()
    pid := os.Getpid()
    random := generateRandomHex(6)
    return fmt.Sprintf("%s-%d-%s", hostname, pid, random)
}
// Example: "worker-node-1-12345-a1b2c3"
```

---

## 6. Queue

**Description**: Queue management interface for job submission, queue operations (pause/resume), and queue inspection. Provides producer API and admin operations.

**Go Structure**:
```go
type Queue struct {
    name        string
    redisClient *redis.Client
    keyBuilder  *KeyBuilder
    scripts     *ScriptLoader
}
```

**Fields**:
- `name`: Queue name identifier. Must match worker queue name for job consumption. Maximum 255 characters.
- `redisClient`: Redis client for queue operations. Must be configured for cluster compatibility.
- `keyBuilder`: Redis key generator with hash tag support (`bull:{queue-name}:*`). Ensures cluster compatibility.
- `scripts`: Lua script loader and executor for atomic queue operations (add, clean, etc.).

**Operations**:
| Operation | Description |
|-----------|-------------|
| `Add(name, data, opts)` | Add job to queue with options (priority, delay, attempts, etc.) |
| `Pause()` | Pause queue (stop job processing, jobs remain in queue) |
| `Resume()` | Resume paused queue (restart job processing) |
| `Clean(grace, limit, status)` | Remove old jobs from completed/failed queues |
| `GetJobCounts()` | Get count of jobs in each state (waiting, active, completed, failed) |
| `GetJob(id)` | Retrieve job by ID from Redis |
| `RemoveJob(id)` | Remove job by ID from queue |
| `Drain()` | Remove all jobs from queue (all states) |

---

## 7. Event

**Description**: Job lifecycle event emitted to Redis stream for monitoring and observability. Events track job state transitions and progress updates.

**Go Structure**:
```go
type Event struct {
    EventType    string                 `json:"event"`
    JobID        string                 `json:"jobId"`
    Timestamp    int64                  `json:"timestamp"`
    AttemptsMade int                    `json:"attemptsMade"`
    Data         map[string]interface{} `json:"data"`
}
```

**Fields**:
- `EventType`: Event type identifier. One of: "waiting", "active", "progress", "completed", "failed", "stalled", "retry".
- `JobID`: Identifier of the job this event relates to. References Job.ID.
- `Timestamp`: Event creation timestamp in Unix milliseconds. Set automatically on event emission.
- `AttemptsMade`: Number of processing attempts at time of event. Increments on retry.
- `Data`: Event-specific payload (varies by event type).

**Event Types**:

**1. waiting**: Job added to queue
```json
{
  "event": "waiting",
  "jobId": "job-123",
  "timestamp": 1698765432000,
  "attemptsMade": 0,
  "data": {"name": "send-email", "priority": 10}
}
```

**2. active**: Job picked up by worker
```json
{
  "event": "active",
  "jobId": "job-123",
  "timestamp": 1698765435000,
  "attemptsMade": 1,
  "data": {"workerId": "worker-node-1-12345-a1b2c3"}
}
```

**3. progress**: Job progress updated
```json
{
  "event": "progress",
  "jobId": "job-123",
  "timestamp": 1698765437000,
  "attemptsMade": 1,
  "data": {"progress": 50}
}
```

**4. completed**: Job completed successfully
```json
{
  "event": "completed",
  "jobId": "job-123",
  "timestamp": 1698765440000,
  "attemptsMade": 1,
  "data": {"returnvalue": {"emailId": "email-456"}, "duration": 5000}
}
```

**5. failed**: Job failed permanently
```json
{
  "event": "failed",
  "jobId": "job-123",
  "timestamp": 1698765440000,
  "attemptsMade": 3,
  "data": {"failedReason": "SMTP timeout", "stacktrace": ["..."]}
}
```

**6. stalled**: Job lock expired
```json
{
  "event": "stalled",
  "jobId": "job-123",
  "timestamp": 1698765440000,
  "attemptsMade": 1,
  "data": {"reason": "lock_expired", "lockDuration": 30000}
}
```

**7. retry**: Job retrying after transient error
```json
{
  "event": "retry",
  "jobId": "job-123",
  "timestamp": 1698765440000,
  "attemptsMade": 2,
  "data": {"error": "Network timeout", "delay": 2000, "backoffType": "exponential"}
}
```

**Redis Stream Format**:
```
XADD bull:{myqueue}:events MAXLEN ~ 10000 * event waiting jobId job-123 timestamp 1698765432000 ...
```

---

## 8. Error Types

**Description**: Error categorization for retry vs fail-fast logic. Categorizes errors as transient (retry) or permanent (fail immediately).

**Go Structure**:
```go
type ErrorCategory int

const (
    ErrorCategoryPermanent ErrorCategory = iota
    ErrorCategoryTransient
)

type TransientError struct {
    Err error
    Msg string
}

func (e *TransientError) Error() string {
    return fmt.Sprintf("transient error: %s: %v", e.Msg, e.Err)
}

type PermanentError struct {
    Err error
    Msg string
}

func (e *PermanentError) Error() string {
    return fmt.Sprintf("permanent error: %s: %v", e.Msg, e.Err)
}
```

**Transient Errors** (retry with backoff):
- Network errors: connection refused, timeout, DNS failure
- Redis errors: connection lost, READONLY, LOADING
- HTTP 5xx errors: server error, service unavailable
- Resource exhaustion: too many files, out of memory
- Rate limiting: 429 Too Many Requests

**Permanent Errors** (fail immediately):
- Validation errors: invalid input, missing field
- HTTP 4xx errors: bad request, unauthorized, forbidden
- Business logic errors: insufficient funds, duplicate transaction
- Serialization errors: invalid JSON
- Configuration errors: missing API key

**Categorization Function**:
```go
func CategorizeError(err error) ErrorCategory {
    if err == nil {
        return ErrorCategoryPermanent
    }

    var transientErr *TransientError
    if errors.As(err, &transientErr) {
        return ErrorCategoryTransient
    }

    var permanentErr *PermanentError
    if errors.As(err, &permanentErr) {
        return ErrorCategoryPermanent
    }

    if isNetworkError(err) || isRedisError(err) {
        return ErrorCategoryTransient
    }

    if httpErr, ok := err.(*HTTPError); ok {
        if httpErr.StatusCode >= 500 {
            return ErrorCategoryTransient
        }
        return ErrorCategoryPermanent
    }

    return ErrorCategoryPermanent
}
```

---

## 9. Redis Key Schema

**Description**: Redis key naming conventions with hash tags for cluster compatibility. All keys for a queue must hash to the same Redis Cluster slot.

**Key Pattern**: `bull:{queue-name}:suffix`

**Hash Tag**: `{queue-name}` ensures all keys land in same cluster slot

**Key Types**:

| Key | Type | Description |
|-----|------|-------------|
| `bull:{queue}:wait` | LIST | FIFO queue for jobs without priority |
| `bull:{queue}:prioritized` | ZSET | Priority-based queue (score = priority) |
| `bull:{queue}:delayed` | ZSET | Scheduled jobs (score = timestamp) |
| `bull:{queue}:active` | LIST | Currently processing jobs |
| `bull:{queue}:completed` | ZSET | Completed jobs (score = finishedOn) |
| `bull:{queue}:failed` | ZSET | Failed jobs (score = finishedOn) |
| `bull:{queue}:events` | STREAM | Job lifecycle events |
| `bull:{queue}:meta` | HASH | Queue metadata (paused, rate limits) |
| `bull:{queue}:{jobId}` | HASH | Job data |
| `bull:{queue}:{jobId}:lock` | STRING | Job lock token (TTL = LockDuration) |
| `bull:{queue}:{jobId}:logs` | LIST | Job log entries (trimmed to max 100) |

**Go Implementation**:
```go
type KeyBuilder struct {
    queueName string
}

func (kb *KeyBuilder) Wait() string {
    return fmt.Sprintf("bull:{%s}:wait", kb.queueName)
}

func (kb *KeyBuilder) Prioritized() string {
    return fmt.Sprintf("bull:{%s}:prioritized", kb.queueName)
}

func (kb *KeyBuilder) Job(jobID string) string {
    return fmt.Sprintf("bull:{%s}:%s", kb.queueName, jobID)
}

func (kb *KeyBuilder) Lock(jobID string) string {
    return fmt.Sprintf("bull:{%s}:%s:lock", kb.queueName, jobID)
}

func (kb *KeyBuilder) Events() string {
    return fmt.Sprintf("bull:{%s}:events", kb.queueName)
}
```

---

## 10. JobCounts

**Description**: Queue statistics structure returned by `Queue.GetJobCounts()`. Provides snapshot of queue state.

**Go Structure**:
```go
type JobCounts struct {
    Waiting    int64
    Active     int64
    Completed  int64
    Failed     int64
    Delayed    int64
}
```

**Fields**:
- `Waiting`: Jobs in wait or prioritized queues (ready for processing)
- `Active`: Jobs currently being processed
- `Completed`: Jobs that completed successfully
- `Failed`: Jobs that failed permanently
- `Delayed`: Jobs scheduled for future processing

---

## 11. Lock Token

**Description**: Unique token for job ownership. Prevents duplicate processing by multiple workers. Uses UUID v4 for cryptographic randomness.

**Security Requirements**:
- MUST use UUID v4 (cryptographically random)
- MUST NOT use UUID v1 (timestamp-based, predictable)
- Prevents lock hijacking attacks

**Go Structure**:
```go
type LockToken string

func NewLockToken() LockToken {
    return LockToken(uuid.New().String())
}
```

**Usage Flow**:
1. Worker picks up job from queue (moveToActive.lua)
2. Lua script generates lock token and stores in `bull:{queue}:{jobId}:lock` with TTL
3. Worker passes lock token to all subsequent operations (heartbeat, complete, fail)
4. Redis validates lock token matches before allowing operation
5. Lock token expires after LockDuration (30s default) if not renewed

**Redis Storage**:
```
Key: bull:{myqueue}:123:lock
Value: "550e8400-e29b-41d4-a716-446655440000" (UUID v4)
TTL: 30000 (30 seconds)
```

---

## Performance Constraints

- Job hash reads: < 10ms (HGETALL)
- Lock acquisition: < 5ms (Lua script)
- Event emission: < 5ms (XADD)
- Stalled check cycle: < 100ms (scan :active)

## Data Constraints

- **Max job payload size**: 10MB (job.Data + job.Opts serialized)
- **Max log entries per job**: 1000 (LTRIM enforced)
- **Max events per stream**: ~10,000 (MAXLEN approximate trim)
- **Job TTL**: Configurable via removeOnComplete/Fail

## Concurrency Constraints

- Atomic state transitions via Lua scripts
- Lock token prevents duplicate processing
- Stalled detection resolves lock conflicts

---

**Status**: ✅ Data Model Complete (Phase 1 Requirements)
**Next**: Implement types in `pkg/bullmq/` package

**Entities Defined**:
1. Job (core job data structure)
2. JobOptions (job configuration)
3. BackoffConfig (retry backoff strategy)
4. Worker (job consumer)
5. WorkerOptions (worker configuration)
6. Queue (producer and queue management)
7. Event (lifecycle events)
8. Error Types (transient vs permanent)
9. Redis Key Schema (cluster-compatible keys)
10. JobCounts (queue statistics)
11. Lock Token (job ownership security)
