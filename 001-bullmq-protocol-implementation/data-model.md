# Data Model: BullMQ Go Client Library

**Date**: 2025-10-29
**Feature**: 001-bullmq-protocol-implementation
**Phase**: Phase 1 (Design)

---

## Entity Relationship Diagram

```
┌─────────────────┐
│      Job        │
├─────────────────┤
│ ID              │◄──┐
│ Name            │   │
│ Data            │   │
│ Opts            │───┼──► JobOptions
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
│ LockToken       │   │
└─────────────────┘   │
        │             │
        │ 0..*        │
        │             │
        ▼             │
┌─────────────────┐   │
│     Event       │   │
├─────────────────┤   │
│ Event           │   │
│ JobID           │───┘
│ Timestamp       │
│ AttemptsMade    │
│ Data            │
└─────────────────┘

┌─────────────────┐
│   JobOptions    │
├─────────────────┤
│ Priority        │
│ Delay           │
│ Attempts        │
│ Backoff         │───► BackoffConfig
│ RemoveOnComplete│
│ RemoveOnFail    │
└─────────────────┘

┌─────────────────┐
│ BackoffConfig   │
├─────────────────┤
│ Type            │
│ Delay           │
└─────────────────┘
```

---

## Entity: Job

### Description

Represents a job in BullMQ protocol format. This is the primary entity for job processing and state management.

### Fields

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| **ID** | string | Required, Unique | Job identifier (UUID or custom) |
| **Name** | string | Required | Job type/name (e.g., "send-email", "process-payment") |
| **Data** | map[string]interface{} | Required | Job payload (arbitrary JSON-serializable data) |
| **Opts** | JobOptions | Required | Job configuration options |
| **Progress** | int | 0-100 | Job completion percentage |
| **Delay** | int64 | ≥ 0 (milliseconds) | Delay before processing |
| **Timestamp** | int64 | Required, Unix ms | Job creation time |
| **AttemptsMade** | int | ≥ 0, ≤ Opts.Attempts | Current attempt number |
| **ProcessedOn** | int64 | Unix ms or 0 | When processing started |
| **FinishedOn** | int64 | Unix ms or 0 | When job finished |
| **WorkerID** | string | Optional | Worker that processed the job (format: `{hostname}-{pid}-{random}`) |
| **ReturnValue** | interface{} | Optional | Result on success |
| **FailedReason** | string | Optional | Error message on failure |
| **Stacktrace** | []string | Optional | Error stack on failure |
| **LockToken** | string | Required (internal), UUID | Lock ownership token |

### Validation Rules

1. **ID uniqueness**: Enforced by Redis (SETNX on job creation)
2. **Required fields**: ID, Name, Data, Opts, Timestamp must be non-empty
3. **Data structure**: Can contain any JSON-serializable data (library-agnostic)
4. **Progress range**: 0 ≤ Progress ≤ 100
5. **Attempt limit**: AttemptsMade ≤ Opts.Attempts (otherwise move to DLQ)
6. **Mutual exclusivity**: Only one of ReturnValue (success) or FailedReason (failure) should be set
7. **Max payload size**: Job data + opts MUST be <= 10MB after JSON serialization
   - **Rationale**: Redis string value limit is 512MB, but large payloads impact performance
   - **Enforcement**: Check serialized size BEFORE Redis write (fail fast)
   - **Error format**: "Job payload size 12.3 MB exceeds limit of 10.0 MB"
   - **Calculation**: `len(json.Marshal(job.Data)) + len(json.Marshal(job.Opts)) <= 10 * 1024 * 1024`
   - **Alternative**: Store large payloads in S3/GCS, put reference URL in job.Data

### State Transitions

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

### Redis Storage

**Hash Key**: `bull:{queue}:{jobId}`

**Hash Fields**:
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

### Example Payloads

**Example 1: Send Email Job**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "send-email",
  "data": {
    "to": "user@example.com",
    "subject": "Welcome!",
    "body": "Thank you for signing up."
  },
  "opts": {
    "priority": 5,
    "attempts": 3,
    "backoff": {
      "type": "exponential",
      "delay": 1000
    }
  }
}
```

**Example 2: Process Payment Job**
```json
{
  "id": "payment-789",
  "name": "process-payment",
  "data": {
    "orderId": "order-123",
    "amount": 99.99,
    "currency": "USD",
    "paymentMethod": "credit_card"
  },
  "opts": {
    "priority": 10,
    "attempts": 5,
    "backoff": {
      "type": "exponential",
      "delay": 2000
    }
  }
}
```

---

## Entity: JobOptions

### Description

Configuration options for job processing behavior.

### Fields

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| **Priority** | int | ≥ 0, default: 0 | Job priority (higher = earlier) |
| **Delay** | int64 | ≥ 0 (milliseconds), default: 0 | Delay before processing |
| **Attempts** | int | ≥ 1, default: 3 | Max retry attempts |
| **Backoff** | BackoffConfig | Required | Retry backoff strategy |
| **RemoveOnComplete** | bool or int | default: false | Job cleanup on success |
| **RemoveOnFail** | bool or int | default: false | Job cleanup on failure |

### Validation Rules

1. **Priority**:
   - MUST be >= 0 (non-negative integer)
   - Negative values: REJECT with validation error
   - 0 = no priority (job added to `wait` queue)
   - > 0 = priority processing (job added to `prioritized` queue)

2. **Delay**:
   - MUST be >= 0 (non-negative milliseconds)
   - Negative values: REJECT with validation error
   - 0 = process immediately
   - > 0 = delay processing (job added to `delayed` queue)

3. **Attempts**:
   - MUST be >= 1 (at least one attempt)
   - 0 or negative: REJECT with validation error
   - Prevents infinite retries (must fail eventually)

4. **Backoff.Type**:
   - MUST be "fixed" or "exponential"
   - Other values: REJECT with validation error

5. **Backoff.Delay**:
   - MUST be > 0 (positive milliseconds)
   - 0 or negative: REJECT with validation error

6. **RemoveOnComplete/Fail**:
   - `true`: Delete job hash immediately after completion/failure
   - `false`: Keep job hash indefinitely (manual cleanup required)
   - `0`: Same as `true` (delete immediately, keep zero jobs)
   - `number > 0`: Keep last N completed/failed jobs (FIFO eviction when count exceeded)
   - `number < 0`: REJECT with validation error
   - `number > 10000`: WARN (performance impact) or REJECT based on policy

**Edge Case Semantics**:
- `RemoveOnComplete: 0` → Delete immediately (equivalent to `true`)
- `RemoveOnComplete: 1` → Keep only the most recent job (delete older ones)
- `RemoveOnComplete: false` → Keep all jobs (no automatic cleanup)

### Semantics

- **Priority 0**: Job added to `bull:{queue}:wait` (LIST, FIFO)
- **Priority > 0**: Job added to `bull:{queue}:prioritized` (ZSET, score = priority)
- **Delay > 0**: Job added to `bull:{queue}:delayed` (ZSET, score = timestamp + delay)

---

## Entity: BackoffConfig

### Description

Retry backoff configuration for failed jobs.

### Fields

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| **Type** | string | "fixed" or "exponential" | Backoff strategy |
| **Delay** | int64 | > 0 (milliseconds) | Base delay |

### Validation Rules

1. **Type**: Must be "fixed" or "exponential"
2. **Delay**: Positive integer

### Semantics

- **Fixed**: Always wait `Delay` milliseconds between retries
- **Exponential**: Wait `min(Delay * 2^(attemptsMade-1), MaxDelay)` milliseconds
  - Attempt 1: Delay (e.g., 1000ms = 1s)
  - Attempt 2: Delay * 2 (e.g., 2000ms = 2s)
  - Attempt 3: Delay * 4 (e.g., 4000ms = 4s)
  - ...
  - Attempt 11: Delay * 1024 (1024s = 17 min) → **capped at MaxDelay**
  - **MaxDelay**: 3600000ms (1 hour) - prevents unbounded backoff growth

**Backoff Cap Rationale**:
- Without cap: Attempt 15 would delay 4.5 hours (2^14 * 1s)
- With 1-hour cap: Maximum retry delay is predictable and reasonable
- Failed jobs should move to DLQ after max attempts, not delay indefinitely

**Formula**:
```
actualDelay = min(baseDelay * 2^(attemptsMade-1), 3600000)
```

**Example**:
```go
backoff := BackoffConfig{
    Type:  "exponential",
    Delay: 1000, // 1 second base
}
// Retry delays: 1s, 2s, 4s, 8s, 16s, 32s, 64s, 128s, 256s, 512s, 1024s, ...
// After attempt 12 (2048s > 3600s), capped at 3600s (1 hour)
```

---

## Entity: Event

### Description

Event emitted to Redis Stream for job lifecycle monitoring.

### Fields

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| **Event** | string | Required, enum | Event type |
| **JobID** | string | Required | Associated job ID |
| **Timestamp** | int64 | Required, Unix ms | Event timestamp |
| **AttemptsMade** | int | ≥ 0 | Attempt number when event occurred |
| **Data** | map[string]interface{} | Optional | Event-specific data |

### Event Types

| Event | Trigger | Data Fields |
|-------|---------|-------------|
| **waiting** | Job added to queue | None |
| **active** | Job picked up | `workerId`, `timestamp` |
| **progress** | Progress update | `progress` (0-100), `message` (optional) |
| **completed** | Job succeeded | `returnvalue` (result object) |
| **failed** | Job failed permanently | `failedReason` (error), `stacktrace` (array) |
| **stalled** | Job lock expired | None |
| **retry** | Job retrying after failure | `delay` (backoff delay) |

### Redis Storage

**Stream Key**: `bull:{queue}:events`

**Entry Format with Retention Policy**:
```
XADD bull:{queue}:events MAXLEN ~ 10000 * \
  event "completed" \
  jobId "550e8400-e29b-41d4-a716-446655440000" \
  timestamp "1698765434000" \
  attemptsMade "1" \
  returnvalue "{\"messageId\":\"abc123\"}"
```

**Retention Policy**:
- **MAXLEN ~10000**: Approximate trimming keeps ~10,000 most recent events
- **Why approximate (~)**: Faster than exact trimming (O(1) vs O(N))
- **Rationale**: Prevents unbounded stream growth leading to memory exhaustion
- **Configurable**: Default 10,000, user can override via WorkerOptions.EventsMaxLen
- **Trade-off**: Old events evicted automatically, use external monitoring for long-term history

---

## Key Relationships

### Job → Options (1:1)

Every job has exactly one JobOptions configuration. Options are embedded in the job hash.

### Job → Events (1:many)

A job generates multiple events throughout its lifecycle (waiting, active, progress, completed/failed).

### Job → Lock (1:0..1)

An active job has exactly one lock (stored separately). Completed/failed jobs have no lock.

### Queue → Jobs (1:many)

A queue contains many jobs in various states (wait, active, completed, failed).

---

## Indexes (Redis)

| Index | Type | Purpose | Key Pattern |
|-------|------|---------|-------------|
| **Wait Queue** | LIST | FIFO processing order | `bull:{queue}:wait` |
| **Prioritized Queue** | ZSET | Priority-based processing | `bull:{queue}:prioritized` (score = priority) |
| **Delayed Queue** | ZSET | Scheduled jobs | `bull:{queue}:delayed` (score = timestamp) |
| **Active Jobs** | LIST | Currently processing | `bull:{queue}:active` |
| **Completed Jobs** | ZSET | Successful results | `bull:{queue}:completed` (score = timestamp) |
| **Failed Jobs** | ZSET | Failed results (DLQ) | `bull:{queue}:failed` (score = timestamp) |

**Compound Operations**:
- **Stalled Detection**: Scan `:active` + check `:lock` existence
- **Rate Limiting**: Check `:limiter` before `moveToActive`
- **Pause**: Check `:meta` pause state before `moveToActive`

---

## Constraints Summary

### Performance Constraints

- Job hash reads: < 10ms (HGETALL)
- Lock acquisition: < 5ms (Lua script)
- Event emission: < 5ms (XADD)
- Stalled check cycle: < 100ms (scan :active)

### Data Constraints

- **Max job payload size**: 10MB (enforced by library before Redis write)
  - Redis theoretical limit: 512MB per string value
  - Practical limit: 10MB for performance and queue latency
  - Includes job.Data + job.Opts serialized together
  - Validation error provides clear feedback with actual size
- **Max log entries per job**: 1000 (LTRIM enforced by addLog.lua)
- **Max events per stream**: ~10,000 (MAXLEN approximate trim on XADD)
- **Job TTL**: Configurable via removeOnComplete/Fail (no automatic expiration otherwise)

### Concurrency Constraints

- Atomic state transitions via Lua scripts
- Lock token prevents duplicate processing
- Stalled detection resolves lock conflicts

---

## Go Type Definitions

```go
// Job represents a BullMQ job
type Job struct {
    ID            string                 `json:"id"`
    Name          string                 `json:"name"`
    Data          map[string]interface{} `json:"data"`
    Opts          JobOptions             `json:"opts"`
    Progress      int                    `json:"progress,omitempty"`
    Delay         int64                  `json:"delay,omitempty"`
    Timestamp     int64                  `json:"timestamp"`
    AttemptsMade  int                    `json:"attemptsMade,omitempty"`
    ProcessedOn   int64                  `json:"processedOn,omitempty"`
    FinishedOn    int64                  `json:"finishedOn,omitempty"`
    WorkerID      string                 `json:"workerId,omitempty"`
    ReturnValue   interface{}            `json:"returnvalue,omitempty"`
    FailedReason  string                 `json:"failedReason,omitempty"`
    Stacktrace    []string               `json:"stacktrace,omitempty"`
    LockToken     string                 `json:"-"` // Internal, not serialized
}

// JobOptions configures job behavior
type JobOptions struct {
    Priority         int           `json:"priority,omitempty"`
    Delay            int64         `json:"delay,omitempty"`
    Attempts         int           `json:"attempts,omitempty"`
    Backoff          BackoffConfig `json:"backoff,omitempty"`
    RemoveOnComplete interface{}   `json:"removeOnComplete,omitempty"` // bool or int
    RemoveOnFail     interface{}   `json:"removeOnFail,omitempty"`     // bool or int
}

// BackoffConfig defines retry backoff strategy
type BackoffConfig struct {
    Type  string `json:"type"`  // "fixed" or "exponential"
    Delay int64  `json:"delay"` // milliseconds
}

// Event represents a job lifecycle event
type Event struct {
    Event        string                 `json:"event"`
    JobID        string                 `json:"jobId"`
    Timestamp    int64                  `json:"timestamp"`
    AttemptsMade int                    `json:"attemptsMade,omitempty"`
    Data         map[string]interface{} `json:"data,omitempty"`
}
```

---

**Status**: ✅ Data Model Complete
**Next**: Implement types in `pkg/bullmq/job.go`
