# Research: BullMQ Protocol Implementation

**Date**: 2025-10-28
**Feature**: 001-bullmq-protocol-implementation
**Phase**: Phase 0 (Research & Design Decisions)

---

## Research Task 1: BullMQ Lua Scripts Analysis

### Decision

Extract and port 8 Lua scripts from BullMQ Node.js repository for atomic Redis operations.

### Rationale

- **Atomicity**: Lua scripts execute atomically, preventing race conditions in concurrent job processing
- **Battle-tested**: BullMQ scripts have 13M+ downloads/month, extensively tested in production
- **Protocol compliance**: Using official scripts ensures 100% compatibility with Node.js BullMQ
- **Edge case handling**: Scripts handle pause state, rate limiting, priorities, delays that manual operations would miss

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| **Manual Redis commands** | Not atomic, race conditions on job state transitions |
| **MULTI/EXEC transactions** | Less flexible for conditional logic, no loops/functions |
| **Write custom scripts** | High risk of edge case bugs, deviation from standard protocol |

### Required Scripts

| Script | Purpose | Source |
|--------|---------|--------|
| `moveToActive.lua` | Atomic job pickup with lock acquisition, respects pause/rate limits | [BullMQ src/scripts](https://github.com/taskforcesh/bullmq/blob/master/src/scripts/moveToActive-*.lua) |
| `moveToCompleted.lua` | Move job to completed, store result, handle removeOnComplete | [BullMQ src/scripts](https://github.com/taskforcesh/bullmq/blob/master/src/scripts/moveToCompleted-*.lua) |
| `moveToFailed.lua` | Move job to failed, store error, handle removeOnFail | [BullMQ src/scripts](https://github.com/taskforcesh/bullmq/blob/master/src/scripts/moveToFailed-*.lua) |
| `retryJob.lua` | Retry with exponential backoff, increment attempts | [BullMQ src/scripts](https://github.com/taskforcesh/bullmq/blob/master/src/scripts/retryJob-*.lua) |
| `moveStalledJobsToWait.lua` | Detect expired locks, requeue jobs atomically | [BullMQ src/scripts](https://github.com/taskforcesh/bullmq/blob/master/src/scripts/moveStalledJobsToWait-*.lua) |
| `extendLock.lua` | Extend lock only if token matches (heartbeat) | Simple custom script (not in BullMQ, trivial) |
| `updateProgress.lua` | Update progress field + emit event atomically | [BullMQ src/scripts](https://github.com/taskforcesh/bullmq/blob/master/src/scripts/updateProgress-*.lua) |
| `addLog.lua` | Append to job logs with trimming | [BullMQ src/scripts](https://github.com/taskforcesh/bullmq/blob/master/src/scripts/addLog-*.lua) |

### Implementation Notes

**Script Loading in Go**:

```go
// internal/bullmq/scripts/scripts.go
const MoveToActive = `
-- Extract from BullMQ repository
-- KEYS[1]: wait key, KEYS[2]: active key, KEYS[3]: prioritized key, ...
-- ARGV[1]: timestamp, ARGV[2]: token, ARGV[3]: lockDuration, ...
[actual script content]
`

// Usage in reader.go
func (r *Reader) GetNextJob(ctx context.Context) (*BullMQJob, error) {
    keys := []string{
        r.keys.Wait(),
        r.keys.Active(),
        r.keys.Prioritized(),
        // ...
    }
    args := []interface{}{
        time.Now().UnixMilli(),
        uuid.New().String(), // lock token
        r.lockTTL.Milliseconds(),
        // ...
    }
    result, err := r.redis.Eval(ctx, scripts.MoveToActive, keys, args...).Result()
    // Parse result
}
```

**Version Pinning**:
- **Exact Version**: v5.62.0 (released 2025-10-28)
- **Commit SHA**: `6a31e0aeab1311d7d089811ede7e11a98b6dd408`
- **Rationale**: Pinning to exact commit prevents protocol drift, ensures reproducible builds, and provides clear upgrade path
- **Verification**: CI job should validate that Lua scripts match upstream on this exact commit
- **Upgrade Strategy**: When upgrading BullMQ version, run full compatibility test suite and document any protocol changes

---

## Research Task 2: Redis Key Pattern Analysis

### Decision

Use hash tags `{queue-name}` in all Redis keys for cluster compatibility.

### Rationale

- **Cluster compatibility**: Hash tags ensure all queue operations hit same Redis Cluster slot
- **Atomic operations**: Multi-key Lua scripts require keys in same slot
- **Standard practice**: BullMQ uses hash tags by default
- **Future-proof**: Works with both single-node and clustered Redis

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| **No hash tags** | Breaks in Redis Cluster (multi-key ops span slots) |
| **Separate key prefix only** | Doesn't guarantee same slot in cluster |

### Complete Key Mapping

| Key Pattern | Type | Purpose | Lifecycle |
|-------------|------|---------|-----------|
| `bull:{queue}:wait` | LIST | Jobs waiting for processing (FIFO, no priority) | RPUSH on add, LPOP on pickup |
| `bull:{queue}:prioritized` | ZSET | Jobs with priority (score = priority) | ZADD on add, ZPOPMIN on pickup |
| `bull:{queue}:delayed` | ZSET | Jobs scheduled for future (score = timestamp) | ZADD on add, moved to wait/prioritized when ready |
| `bull:{queue}:active` | LIST | Jobs currently being processed | RPUSH on pickup, LREM on complete/fail/stalled |
| `bull:{queue}:completed` | ZSET | Successfully completed jobs (score = timestamp) | ZADD on completion, trimmed by removeOnComplete |
| `bull:{queue}:failed` | ZSET | Permanently failed jobs (score = timestamp) | ZADD on failure, trimmed by removeOnFail |
| `bull:{queue}:paused` | LIST | Jobs added while queue paused | RPUSH when paused, moved to wait when resumed |
| `bull:{queue}:{jobId}` | HASH | Job data and metadata | HMSET on creation, updated during processing, DEL by removeOn* |
| `bull:{queue}:{jobId}:lock` | STRING | Job lock with token | SET PX on pickup, PEXPIRE on heartbeat, DEL on complete |
| `bull:{queue}:{jobId}:logs` | LIST | Job log entries | RPUSH on addLog, LTRIM to max size |
| `bull:{queue}:meta` | HASH | Queue metadata (paused state, etc.) | HSET paused 0/1 |
| `bull:{queue}:events` | STREAM | Job lifecycle events | XADD with MAXLEN ~10000 on state changes |
| `bull:{queue}:id` | STRING | Job ID counter | INCR on job creation |
| `bull:{queue}:limiter` | HASH | Rate limiter state (tokens, timestamp) | Updated by rate limiter logic |

**Out of Scope** (MVP):

- `bull:{queue}:waiting-children` (job dependencies)
- `bull:{queue}:repeat` (repeatable jobs)

### Implementation

```go
// internal/bullmq/keys.go
type KeyBuilder struct {
    prefix    string // "bull"
    queueName string // "video-generation"
}

func (k *KeyBuilder) Wait() string {
    return fmt.Sprintf("%s:{%s}:wait", k.prefix, k.queueName)
}
// ... (see BULLMQ_CODE_REFERENCE.md for complete implementation)
```

---

## Research Task 3: Error Classification Patterns

### Decision

Categorize all errors as **transient** (retry) or **permanent** (fail immediately).

### Rationale

- **Avoid wasted retries**: Permanent errors (invalid payload, auth) will never succeed
- **Improve UX**: Fast feedback for user errors vs automatic recovery for transient failures
- **Reduce queue load**: Don't clog queue with unretryable jobs

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| **Retry all errors** | Wastes retries on unrecoverable errors, delays user feedback |
| **Never retry** | Loses jobs on transient network/Redis issues |
| **Manual error codes** | Brittle, requires updating for every new error type |

### Error Categories

**Transient Errors** (retry with exponential backoff):

```go
// Network errors
- context.DeadlineExceeded (timeout)
- io.EOF, io.ErrUnexpectedEOF (connection closed)
- syscall.ECONNRESET, ECONNREFUSED, ETIMEDOUT
- net.Error with Timeout() == true or Temporary() == true

// Redis errors
- redis.TxFailedErr (transaction conflict - retry)
- Redis connection errors

// External service errors
- HTTP 5xx (server errors)
- Database connection errors
- Message queue errors (RabbitMQ, Kafka timeouts)
- Cloud storage errors (S3, GCS 503 Service Unavailable)

// Process errors
- Out of memory (temporary)
- Resource temporarily unavailable
```

**Permanent Errors** (fail immediately, no retry):

```go
// Validation errors
- Missing required payload fields
- Invalid data types
- Failed schema validation

// Auth/permission errors
- HTTP 401 Unauthorized, 403 Forbidden
- Invalid API credentials

// Resource not found
- HTTP 404 Not Found
- Missing required resources

// Business logic errors
- Invalid business rules
- Data integrity violations
- Unsupported operations
```

### Implementation

```go
// pkg/bullmq/errors.go
func CategorizeError(err error) ErrorCategory {
    // Context errors
    if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
        return ErrorCategoryTransient
    }

    // Network errors
    var netErr net.Error
    if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
        return ErrorCategoryTransient
    }

    // HTTP status codes
    if httpErr, ok := err.(*HTTPError); ok {
        return httpErr.StatusCode >= 500 ? ErrorCategoryTransient : ErrorCategoryPermanent
    }

    // Validation errors
    if _, ok := err.(*ValidationError); ok {
        return ErrorCategoryPermanent
    }

    // Redis errors
    if errors.Is(err, redis.TxFailedErr) {
        return ErrorCategoryTransient
    }

    // Default: permanent (safe choice to avoid infinite retries)
    return ErrorCategoryPermanent
}
```

### Exponential Backoff with Maximum Cap

**Problem**: Unbounded exponential backoff leads to unreasonable delays
- Attempt 11: 2^10 * 1000ms = 1024s (17 minutes)
- Attempt 15: 2^14 * 1000ms = 16384s (4.5 hours)

**Solution**: Cap maximum delay at 1 hour (3600000ms)

**Formula**:
```go
func CalculateBackoff(baseDelay int64, attemptsMade int, maxDelay int64) int64 {
    if attemptsMade < 1 {
        return baseDelay
    }

    delay := baseDelay
    for i := 1; i < attemptsMade; i++ {
        delay *= 2
        if delay >= maxDelay {
            return maxDelay // Cap reached
        }
    }
    return delay
}

// Default: maxDelay = 3600000ms (1 hour)
```

**Example Delays** (baseDelay=1000ms, maxDelay=3600000ms):
- Attempt 1: 1s
- Attempt 5: 16s
- Attempt 10: 512s (8.5 min)
- Attempt 12: 3600s (capped at 1 hour)
- Attempt 20: 3600s (capped at 1 hour)

### Testing Strategy

**Test Cases**:

1. Network timeout → Transient
2. HTTP 503 → Transient
3. HTTP 404 → Permanent
4. Invalid JSON payload → Permanent
5. Redis connection lost → Transient
6. Redis transaction conflict → Transient
7. Validation error → Permanent
8. Auth error (HTTP 401) → Permanent
9. **Exponential backoff cap**: Verify attempt 15 delays max 1 hour (not 4.5 hours)

---

## Research Task 4: Heartbeat & Stalled Detection Timing

### Decision

- **Lock TTL**: 30 seconds
- **Heartbeat Interval**: 15 seconds (50% of TTL)
- **Stalled Check Interval**: 30 seconds

### Rationale

**Lock TTL = 30s**:

- Long enough to tolerate brief network hiccups (5-10s)
- Short enough for fast recovery (stalled detected within 60s)
- Suitable for most job types (short and long-running jobs)

**Heartbeat Interval = 15s**:

- Standard practice: heartbeat at 50% of lock TTL
- Provides 1 retry opportunity if heartbeat fails
- Low overhead (~10ms per extension × 4 extensions/min = 40ms/min overhead)

**Stalled Check = 30s**:

- Frequent enough to detect failures within ~60s (2 check cycles)
- Not too frequent to cause Redis load (script scans active list)
- Independent of worker pool size

**Long Scan Handling** (when scan duration > interval):

**Problem**: With 10,000+ active jobs, scanning takes > 100ms potentially > 30s
- Lua script blocks Redis during execution
- Overlapping cycles waste resources
- Could delay other operations

**Solution**: Skip cycle if previous cycle still running

```go
type StalledChecker struct {
    interval time.Duration // 30s
    running  atomic.Bool
}

func (sc *StalledChecker) Run(ctx context.Context) {
    ticker := time.NewTicker(sc.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            // Skip if previous cycle still running
            if !sc.running.CompareAndSwap(false, true) {
                metrics.StalledCheckerSkipped.Inc()
                log.Warn("Stalled checker cycle skipped (previous cycle still running)")
                continue
            }

            go func() {
                defer sc.running.Store(false)
                sc.checkStalledJobs(ctx)
            }()

        case <-ctx.Done():
            return
        }
    }
}
```

**Performance Considerations**:
- Target: < 100ms for 10,000 active jobs
- If consistently exceeding, consider:
  1. Cursor-based iteration (batch process in chunks)
  2. Increase check interval to 60s
  3. Partition queue across multiple Redis instances

### Alternatives Considered

| Configuration | Rejected Because |
|--------------|------------------|
| Lock TTL = 60s, Heartbeat = 30s | Slower recovery (stalled detection takes 90-120s) |
| Lock TTL = 15s, Heartbeat = 7s | Too much heartbeat overhead, sensitive to network jitter |
| Stalled Check = 60s | Too slow for user-facing service (2-minute recovery) |
| Stalled Check = 10s | Unnecessary Redis load, no significant benefit |

### Edge Cases

**Case 1: Worker crash mid-job**

- Lock not extended → expires after 30s
- Stalled checker runs after 30s (worst case 60s) → job requeued
- Result: Job reprocessed within 30-60s, no loss

**Case 2: Network partition during heartbeat**

- Heartbeat fails → logged but job continues processing
- Lock expires after 30s
- Stalled checker requeues job (duplicate processing possible)
- Result: Acceptable if job processing is idempotent (user's responsibility)

**Case 3: Job completes just before lock expiry**

- Completion script removes from active + deletes lock atomically
- Stalled checker sees job not in active → no action
- Result: No conflict

**Case 4: Long-running job (90s)**

- Heartbeat runs 6 times (0s, 15s, 30s, 45s, 60s, 75s)
- Lock never expires
- Result: Job completes normally

### Implementation

```go
// pkg/bullmq/worker.go
type WorkerOptions struct {
    LockDuration         time.Duration // default: 30s
    HeartbeatInterval    time.Duration // default: 15s
    StalledCheckInterval time.Duration // default: 30s
    Concurrency          int           // default: 1
    MaxAttempts          int           // default: 3
    BackoffDelay         time.Duration // default: 1s
}
```

### Heartbeat Failure Handling

**Policy**: Continue processing despite heartbeat failures (no circuit breaker, no retry limit)

**Rationale**:
- Heartbeat failures are usually transient (network hiccup, Redis spike)
- Stopping job processing proactively wastes work already done
- Stalled checker provides safety net (requeues job if lock expires)
- Idempotency requirement protects against duplicate processing

**Behavior on Heartbeat Failure**:
1. **Log error**: `failed to extend lock for job {jobId}: {error}`
2. **Increment metric**: `bullmq_heartbeat_failure_total{queue="myqueue"}`
3. **Continue processing**: Worker does NOT abort job
4. **No retry limit**: Heartbeat attempts every 15s until job completes
5. **Lock expiration**: If 30s pass without successful heartbeat, lock expires
6. **Stalled detection**: Stalled checker requeues job within 30-60s
7. **Race condition**: Original worker may complete job after requeue (idempotency handles)

**Example Timeline**:
```
T=0s:    Job picked up, lock acquired (TTL=30s)
T=15s:   Heartbeat #1 SUCCESS (lock renewed to T=45s)
T=30s:   Heartbeat #2 FAILED (network timeout, lock still valid until T=45s)
T=45s:   Lock expires (no successful heartbeat since T=15s)
T=45s:   Heartbeat #3 FAILED (lock already expired)
T=50s:   Worker completes job, tries to move to completed
T=50s:   moveToCompleted.lua FAILS (lock token mismatch or missing)
T=60s:   Stalled checker requeues job to wait queue
T=65s:   Different worker picks up job, processes again (idempotent handler)
```

**NOT Implemented (by design)**:
- ❌ Circuit breaker (stop after N consecutive failures)
- ❌ Exponential backoff for heartbeat retries
- ❌ Proactive job failure on heartbeat failure
- ❌ Lock ownership validation before completion (Lua script handles)

### Monitoring

**Metrics to track**:

- `heartbeat_extend_failure_total` - If high, network or Redis issues
- `stalled_jobs_detected_total` - If high, worker crashes or locks timing out
- `lock_extend_duration_seconds` - Should be < 10ms

**Alerts**:

- Heartbeat failure rate > 5% → investigate Redis latency
- Stalled jobs > 10% of active jobs → investigate worker stability

---

## Research Task 5: Cross-Language Compatibility Requirements

### Decision

Validate protocol compatibility with Node.js BullMQ v5.x via shadow testing.

### Rationale

- **Protocol drift risk**: Node.js and Go implementations must produce identical Redis state
- **Frontend integration**: Worker must consume jobs exactly as frontend produces them
- **Production confidence**: Shadow testing catches incompatibilities before deployment

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| **Manual inspection** | Error-prone, doesn't scale, no regression detection |
| **Unit tests only** | Doesn't validate actual Node.js interop |
| **Hope for the best** | High risk of production failures |

### Example Payload Format

**Generic Job Payload**:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "send-email",
  "data": {
    "to": "user@example.com",
    "subject": "Welcome!",
    "body": "Thank you for signing up.",
    "userId": "user-123"
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

### Job Processing in Go

```go
// User-defined job processor
worker := bullmq.NewWorker("myqueue", redisClient, bullmq.WorkerOptions{
    Concurrency: 10,
})

worker.Process(func(job *bullmq.Job) error {
    // Access job data (user's responsibility to parse)
    to, _ := job.Data["to"].(string)
    subject, _ := job.Data["subject"].(string)
    body, _ := job.Data["body"].(string)

    // Validate
    if to == "" || subject == "" {
        return &ValidationError{Message: "Missing required fields"}
    }

    // Process job (user's business logic)
    err := sendEmail(to, subject, body)
    if err != nil {
        return err // Library handles retry/failure based on error category
    }

    return nil // Success
})

worker.Start(ctx)
```

### Redis State Validation Table

| Step | Node.js BullMQ | Go Worker | Validation |
|------|----------------|-----------|------------|
| **Job submission** | INCR id, HMSET hash, ZADD prioritized, XADD event | N/A (frontend only) | N/A |
| **Job pickup** | `moveToActive.lua` → job data | `moveToActive.lua` → job data | Redis diff shows identical state |
| **Lock acquisition** | SET lock:{jobId} {token} PX 30000 | SET lock:{jobId} {token} PX 30000 | Lock key format identical |
| **Progress update** | HSET {jobId} progress 50, XADD event | HSET {jobId} progress 50, XADD event | Hash field + event identical |
| **Completion** | LREM active, DEL lock, HMSET returnvalue, ZADD completed, XADD event | Same operations | Redis diff shows identical state |

### Compatibility Test Plan

**Test 1: Node.js Producer → Go Consumer**

```javascript
// tests/compatibility/node-producer.js
const { Queue } = require('bullmq');
const queue = new Queue('test-queue', { connection: redisConfig });

await queue.add('send-email', {
  to: 'test@example.com',
  subject: 'Test',
  body: 'Compatibility test'
});

// Wait for Go worker to process
// Verify: job completed, Redis state matches expectations
```

**Test 2: Go Producer → Node.js Consumer** (validation)

```go
// pkg/bullmq/compatibility_test.go
func TestGoProducerNodeConsumer(t *testing.T) {
    // Go produces job to Redis
    queue := bullmq.NewQueue("test-queue", redisClient)
    job, err := queue.Add("send-email", map[string]interface{}{
        "to":      "test@example.com",
        "subject": "Test",
        "body":    "Compatibility test",
    }, bullmq.JobOptions{})

    // Node.js worker consumes (run via exec)
    // Verify: Node.js can parse job, Redis state matches
}
```

**Test 3: Shadow Worker Test** (parallel processing)

```bash
# Start Go worker
go run examples/worker/main.go &

# Start Node.js worker
node tests/compatibility/node-worker.js &

# Submit 10 jobs
node tests/compatibility/submit-jobs.js 10

# Verify: all jobs complete, no conflicts, Redis state consistent
```

### Monitoring Strategy

**During shadow test**:

- Compare Redis state snapshots every 10s
- Log any key mismatches (key name, type, value)
- Verify event stream format identical (XRANGE comparison)

**Metrics**:

- Jobs processed by Go vs Node.js (should be split)
- Error rate comparison (should be equal)
- Latency comparison (Go should be ≤ Node.js)

---

## Research Task 6: Redis Connection Loss Retry Strategy

### Decision

Unlimited reconnection attempts with exponential backoff + jitter (default), configurable max attempts.

### Rationale

- **Redis downtime is usually temporary**: Restarts, failovers, network blips resolve within seconds/minutes
- **Worker should survive**: Don't kill worker on transient Redis issues
- **Exponential backoff**: Prevents overwhelming Redis during recovery
- **Jitter**: Prevents thundering herd when multiple workers reconnect simultaneously
- **Configurable limits**: Allow users to fail fast in edge cases (e.g., ephemeral test workers)

### Alternatives Considered

| Alternative | Rejected Because |
|-------------|------------------|
| **Fixed retry interval** | Hammers Redis during outage, no backoff |
| **Limited retries (e.g., 5)** | Worker dies on brief Redis restart (< 30s downtime common) |
| **No reconnection** | Worker becomes useless after first disconnect |

### Retry Formula

```go
func calculateReconnectDelay(attempt int, initialDelay, maxDelay time.Duration) time.Duration {
    // Exponential backoff: initialDelay * 2^attempt
    delay := initialDelay
    for i := 0; i < attempt && delay < maxDelay; i++ {
        delay *= 2
    }
    if delay > maxDelay {
        delay = maxDelay
    }

    // Add jitter: ±20% to prevent thundering herd
    jitter := 0.8 + 0.4*rand.Float64() // 0.8 to 1.2
    return time.Duration(float64(delay) * jitter)
}

// Initial: 100ms, Max: 30s
// Attempt 1:  ~100ms
// Attempt 5:  ~1.6s
// Attempt 10: ~30s (capped)
```

### Behavior During Disconnect

1. **Stop new job pickup**: Don't try to call `moveToActive.lua` (will fail)
2. **Continue active jobs**: Use cached job data, complete if possible
3. **Heartbeat failures**: Log but continue (stalled checker handles)
4. **Background reconnection**: Goroutine retries with backoff
5. **Resume on success**: Start picking jobs again

### Implementation Notes

```go
type Worker struct {
    redis         *redis.Client
    connected     atomic.Bool
    reconnectOpts ReconnectOptions
}

type ReconnectOptions struct {
    InitialDelay time.Duration // 100ms
    MaxDelay     time.Duration // 30s
    MaxAttempts  int           // 0 = unlimited
}

func (w *Worker) handleDisconnect() {
    w.connected.Store(false)
    attempt := 0

    for {
        if w.reconnectOpts.MaxAttempts > 0 && attempt >= w.reconnectOpts.MaxAttempts {
            log.Error("Max reconnect attempts reached, shutting down")
            w.Shutdown()
            return
        }

        delay := calculateReconnectDelay(attempt, w.reconnectOpts.InitialDelay, w.reconnectOpts.MaxDelay)
        time.Sleep(delay)

        if err := w.redis.Ping(context.Background()).Err(); err == nil {
            log.Info("Reconnected to Redis")
            w.connected.Store(true)
            metrics.RedisConnectionStatus.Set(1) // connected
            return
        }

        attempt++
        metrics.RedisReconnectAttempts.Inc()
        log.Warn("Reconnect attempt %d failed, retrying in %v", attempt, delay)
    }
}
```

## Summary of Decisions

| Decision Area | Choice | Rationale |
|--------------|--------|-----------|
| **Lua Scripts** | Extract from BullMQ Node.js repo | Atomic, battle-tested, protocol compliant |
| **Redis Keys** | Hash tags `{queue}` in all keys | Cluster compatible, atomic multi-key ops |
| **Error Handling** | Transient vs permanent categorization | Avoid wasted retries, fast user feedback |
| **Lock TTL** | 30 seconds | Balance recovery speed vs network tolerance |
| **Heartbeat Interval** | 15 seconds (50% of TTL) | Standard practice, 1 retry opportunity |
| **Stalled Check** | 30 seconds | Detects failures within 60s, low Redis load |
| **Compatibility** | Shadow testing with Node.js | Validates protocol, catches drift early |
| **Job Cleanup** | Keep jobs (debug mode) | Production override, valuable for troubleshooting |
| **Redis Reconnect** | Unlimited with exponential backoff + jitter | Worker survives temporary Redis downtime |

---

## Next Phase

**Phase 1**: Generate data-model.md and contracts/ based on research findings.

**Status**: ✅ Research Complete
**Blockers**: None
**Dependencies Resolved**: All NEEDS CLARIFICATION items resolved
