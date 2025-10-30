# Research: BullMQ Go Client Library Implementation

**Feature ID**: 001-bullmq-protocol-implementation
**Date**: 2025-10-30
**Status**: Complete

---

## Overview

This document captures the research and technology decisions required to implement a production-ready BullMQ client library for Go. Each decision is backed by rationale, alternatives considered, and implementation notes.

---

## 1. BullMQ Lua Scripts Extraction

**Decision**: Extract Lua scripts directly from BullMQ repository and embed as Go string constants, pinned to specific commit SHA (v5.62.0, commit `6a31e0aeab1311d7d089811ede7e11a98b6dd408`).

**Rationale**:
- **Protocol compatibility**: Official BullMQ Lua scripts are the source of truth for protocol behavior (13M+ downloads/month, battle-tested)
- **Atomicity guarantee**: Lua scripts provide atomic multi-key operations that MULTI/EXEC cannot (conditional logic, loops, functions)
- **Version stability**: Pinning to commit SHA prevents protocol drift and ensures reproducible builds across all development environments
- **No runtime dependencies**: Embedded scripts mean no external file dependencies at runtime (easier deployment, no file I/O)
- **Edge case handling**: Scripts handle pause state, rate limiting, priorities, delays that manual operations would miss

**Alternatives Considered**:

1. **Git submodule to BullMQ repository**
   - **Rejected**: Adds complexity to build process, requires `git clone --recursive`
   - **Issue**: Submodules are notorious for causing confusion in Go module ecosystem
   - **Issue**: Extra build step required to copy scripts from submodule to Go package
   - **Issue**: Brings in 50MB+ of Node.js dependencies we don't need

2. **Vendoring entire BullMQ repository**
   - **Rejected**: Overkill for extracting ~10 Lua scripts
   - **Issue**: License complexity (BullMQ dependencies may have different licenses)
   - **Issue**: Repository size bloat (Node.js tooling, tests, examples not needed)

3. **Manual copy-paste from BullMQ documentation website**
   - **Rejected**: No version tracking, hard to validate correctness
   - **Issue**: Website may show latest version, not pinned version
   - **Issue**: Documentation may be incomplete or simplified

4. **Implement Lua scripts from scratch based on protocol description**
   - **Rejected**: High risk of protocol incompatibility, edge case bugs
   - **Issue**: BullMQ scripts have years of production testing and edge case handling
   - **Issue**: Maintenance burden to keep in sync with protocol evolution
   - **Issue**: Complex scripts (moveToActive.lua is 200+ lines with rate limiting, priorities, delays)

5. **Use MULTI/EXEC Redis transactions instead of Lua scripts**
   - **Rejected**: MULTI/EXEC cannot implement conditional logic needed for BullMQ protocol
   - **Issue**: Cannot check pause state inside transaction
   - **Issue**: Cannot implement rate limiting (requires GET + conditional SET)
   - **Issue**: Less flexible than Lua (no loops, functions, complex logic)

**Implementation Notes**:

- **Pinned Version**: BullMQ v5.62.0 (released 2025-10-28)
- **Commit SHA**: `6a31e0aeab1311d7d089811ede7e11a98b6dd408`
- **Extraction Process**:
  1. Clone BullMQ repository: `git clone https://github.com/taskforcesh/bullmq.git`
  2. Checkout pinned commit: `git checkout 6a31e0aeab1311d7d089811ede7e11a98b6dd408`
  3. Copy scripts from `src/scripts/` to `pkg/bullmq/scripts/`
  4. Convert to Go string constants in `scripts.go`

- **Script Loading**: Use `redis.NewScript()` to cache SHA1 on Redis server (EVALSHA for performance)
  ```go
  // First call: EVAL (sends full script ~5KB)
  // Subsequent calls: EVALSHA (sends only SHA1 40 bytes)
  script := redis.NewScript(scriptSource)
  result, err := script.Run(ctx, client, keys, args).Result()
  ```

- **CI Validation**: Automated check compares embedded scripts to upstream commit on every build
  ```bash
  # .github/workflows/validate-scripts.yml
  - name: Validate Lua scripts match upstream
    run: |
      git clone https://github.com/taskforcesh/bullmq.git /tmp/bullmq
      cd /tmp/bullmq && git checkout 6a31e0aeab1311d7d089811ede7e11a98b6dd408
      diff -r /tmp/bullmq/src/scripts/ pkg/bullmq/scripts/ || exit 1
  ```

- **Update Process**: When upgrading BullMQ version:
  1. Update pinned commit SHA in constitution
  2. Re-extract scripts from new commit
  3. Run full compatibility test suite (Node.js interop)
  4. Document any protocol changes in CHANGELOG.md

**Required Scripts**:
- `moveToActive.lua` - Move job from wait/prioritized to active with lock acquisition (respects pause, rate limits, priorities)
- `moveToCompleted.lua` - Move job from active to completed with result storage (handles removeOnComplete)
- `moveToFailed.lua` - Move job from active to failed with error details (handles removeOnFail)
- `retryJob.lua` - Retry failed job with exponential backoff, increment attemptsMade
- `moveStalledJobsToWait.lua` - Detect expired locks, requeue jobs atomically
- `extendLock.lua` - Extend job lock TTL (heartbeat) only if token matches
- `updateProgress.lua` - Update job progress (0-100) + emit progress event atomically
- `addLog.lua` - Append log entry to job logs list with trimming

**References**:
- [BullMQ Scripts Directory](https://github.com/taskforcesh/bullmq/tree/master/src/scripts)
- [Redis EVAL Documentation](https://redis.io/commands/eval/)
- [Redis Script Caching (EVALSHA)](https://redis.io/commands/evalsha/)

---

## 2. Redis Client Selection (go-redis/v9)

**Decision**: Use `github.com/redis/go-redis/v9` as the Redis client library.

**Rationale**:
- **Industry standard**: Most popular Go Redis client (19k+ GitHub stars, used by Kubernetes, Docker, etc.)
- **Redis Cluster support**: Built-in cluster mode with automatic slot routing (critical for production scalability)
- **Script caching**: Native support for `EVALSHA` (efficient Lua script execution, 100x bandwidth reduction)
- **Connection pooling**: Automatic connection pool management with health checks (zero-config performance)
- **Context support**: Native context.Context for cancellation and timeouts (idiomatic Go, graceful shutdown)
- **Active maintenance**: Regular updates, security patches, Go 1.21+ support
- **Compatibility**: Works with Redis 6.0+ (required for Lua script features we use)
- **Well-documented**: Extensive documentation, examples, community support

**Alternatives Considered**:

1. **github.com/gomodule/redigo**
   - **Rejected**: Legacy client, less active maintenance (last major release 2+ years ago)
   - **Issue**: No built-in Redis Cluster support (requires manual slot calculation, error-prone)
   - **Issue**: Less ergonomic API (manual connection management, no connection pooling)
   - **Issue**: No context.Context support (hard to implement timeouts, cancellation)

2. **github.com/rueian/rueidis**
   - **Considered**: New high-performance client (2021+)
   - **Pros**: Better performance benchmarks (client-side caching, pipelining, connection multiplexing)
   - **Rejected**: Less mature ecosystem, smaller community (2k stars vs 19k)
   - **Issue**: Breaking API changes more frequent (pre-1.0 instability)
   - **Issue**: Less Stack Overflow/GitHub discussion for troubleshooting
   - **Future consideration**: May reconsider if it becomes industry standard (3-5 years)

3. **github.com/go-redis/redis (v8)**
   - **Rejected**: Deprecated, replaced by v9
   - **Issue**: Missing latest Redis features (Streams improvements, ACLs)
   - **Issue**: Import path changed to github.com/redis/go-redis

4. **Implement custom Redis client**
   - **Rejected**: Massive scope increase (RESP protocol, connection pooling, cluster slot routing)
   - **Issue**: Would need months of development and testing
   - **Issue**: Maintenance burden (Redis protocol evolves)

**Implementation Notes**:

**Connection Pooling Configuration**:
```go
client := redis.NewClient(&redis.Options{
    Addr:         "localhost:6379",
    PoolSize:     10 * runtime.GOMAXPROCS(0), // 10 connections per CPU
    MinIdleConns: 2,                           // Keep 2 idle connections warm
    MaxRetries:   3,                           // Retry transient failures
    DialTimeout:  5 * time.Second,            // Connection timeout
    ReadTimeout:  3 * time.Second,            // Command timeout
    WriteTimeout: 3 * time.Second,
    PoolTimeout:  4 * time.Second,            // Wait for connection from pool
})
```

**Script Caching Example**:
```go
script := redis.NewScript(moveToActiveScript)

// First call: EVAL (sends full script ~5KB)
result, err := script.Run(ctx, client, keys, args).Result()

// Subsequent calls: EVALSHA (sends only SHA1 40 bytes, 100x bandwidth reduction)
result, err := script.Run(ctx, client, keys, args).Result()
```

**Cluster Support**:
```go
cluster := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{"node1:6379", "node2:6379", "node3:6379"},
    // go-redis automatically:
    // - Discovers cluster topology
    // - Routes commands to correct slot
    // - Handles MOVED/ASK redirections
    // - Retries on failover
})

// Hash tags ensure all keys for a queue land in same slot
// bull:{myqueue}:wait  → hash "myqueue" → slot 5460
// bull:{myqueue}:active → hash "myqueue" → slot 5460
// Lua scripts work because all keys in same slot
```

**Error Handling Patterns**:
```go
// Key not found (not an error in job queue context)
val, err := client.Get(ctx, key).Result()
if errors.Is(err, redis.Nil) {
    // Key doesn't exist, handle gracefully
}

// Network errors (categorize as transient, trigger retry)
if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
    return &TransientError{Err: err}
}

// CROSSSLOT error (indicates missing hash tags, critical bug)
if strings.Contains(err.Error(), "CROSSSLOT") {
    log.Fatal("CROSSSLOT error: hash tags missing in Redis keys")
}
```

**Best Practices**:
- Use single client instance per application (connection pool is shared, thread-safe)
- Always use context for timeout control (prevents hanging on network issues)
- Check `redis.Nil` explicitly (not all key-not-found cases are errors)
- Monitor connection pool metrics (active connections, idle connections, wait duration)
- Set reasonable timeouts (ReadTimeout, WriteTimeout) to prevent indefinite blocking

**Performance Characteristics**:
- Connection pool overhead: ~1-2µs per command (negligible)
- EVALSHA vs EVAL: 100x bandwidth reduction (5KB → 40 bytes)
- Pipeline batching: 10x throughput increase for bulk operations
- Cluster routing: ~50µs overhead (acceptable for most use cases)

**References**:
- [go-redis Documentation](https://redis.uptrace.dev/)
- [go-redis GitHub](https://github.com/redis/go-redis)
- [Redis Connection Pooling Best Practices](https://redis.io/docs/manual/patterns/connection-pooling/)

---

## 3. Lock Token Generation (UUID v4)

**Decision**: Use UUID v4 (cryptographically random) for lock tokens via `github.com/google/uuid`.

**Rationale**:
- **Security**: UUID v4 uses cryptographic randomness (122 bits of entropy), preventing lock hijacking attacks
- **Uniqueness**: 2^122 possible values = negligible collision probability (1 in 5.3 × 10^36)
- **Standard library**: `github.com/google/uuid` is industry standard (8k+ stars, Google-maintained, used by Kubernetes)
- **Performance**: Lock token generation not in hot path (~1-2µs per token, once per job pickup)
- **Debugging**: Standard UUID format (36 chars with hyphens) familiar to developers

**Security Threat Model**:

**Attack Scenario**: Malicious worker predicts lock token, extends/completes another worker's job

```
Normal Flow:
1. Worker A picks up job, gets lock token "abc123"
2. Worker A extends lock every 15s with token "abc123"
3. Worker A completes job with token "abc123"

Attack with Predictable Tokens (UUID v1 - timestamp-based):
1. Worker A picks up job at T1, gets lock token based on timestamp+MAC
   Token: 550e8400-e29b-11d4-a716-446655440000
2. Attacker observes lock token pattern (timestamp increments predictably)
3. Attacker predicts Worker B's next lock token at T2
   Token: 550e8401-e29b-11d4-a716-446655440000 (predictable increment)
4. Attacker extends/completes Worker B's job before Worker B finishes
5. Result: Job marked complete but never actually processed (data loss)
```

**Why UUID v4 Prevents This**:
- **Unpredictability**: Each token has 122 bits of entropy (2^122 possible values)
- **No pattern**: Cannot predict next token from observing previous tokens
- **Cryptographic randomness**: Uses `/dev/urandom` on Linux or `CryptGenRandom` on Windows
- **Attack infeasible**: Would require 2^61 guesses on average to predict a token (computationally impossible)

**Alternatives Considered**:

1. **UUID v1 (timestamp-based)**
   - **Rejected**: Predictable (timestamp + MAC address + sequence number)
   - **Security risk**: Attacker can predict next token if they observe pattern
   - **Example**: Token at T1 = `550e8400-e29b-11d4-a716-446655440000`, Token at T2 = `550e8401-e29b-11d4-a716-446655440000` (predictable)

2. **Random strings via crypto/rand**
   - **Considered**: `hex.EncodeToString(randomBytes(16))` produces 32-char hex string
   - **Rejected**: Reinventing the wheel, UUID v4 already does this with standard format
   - **Issue**: No standard format, harder to debug/log (is "abc123" a lock token or job ID?)

3. **ULID (Universally Unique Lexicographically Sortable ID)**
   - **Rejected**: Timestamp prefix (48 bits) makes it partially predictable
   - **Security risk**: First 48 bits are timestamp (reduces entropy to 80 bits, still strong but weaker)
   - **Issue**: Sortability not needed for lock tokens (no ordering requirement)

4. **Sequential integers**
   - **Rejected**: Completely predictable, trivial to hijack locks
   - **Example**: Worker A gets token "1234", attacker knows next is "1235"
   - **Security risk**: Total compromise (attacker can hijack any lock)

5. **Short random strings (6-8 characters)**
   - **Rejected**: Insufficient entropy for security (36^8 = 2^41 values, brute-forceable)
   - **Security risk**: Attacker could guess token in millions of attempts

**Implementation Notes**:

```go
import "github.com/google/uuid"

// Generate lock token
token := uuid.New().String() // e.g., "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

// Store lock in Redis
func (w *Worker) AcquireLock(ctx context.Context, jobID, token string, ttl time.Duration) error {
    lockKey := fmt.Sprintf("bull:{%s}:%s:lock", w.queueName, jobID)
    return w.redis.Set(ctx, lockKey, token, ttl).Err()
}

// Validate lock token on completion (Lua script checks)
func (w *Worker) CompleteJob(ctx context.Context, job *Job, token string) error {
    // moveToCompleted.lua checks: GET bull:{queue}:{jobId}:lock == token
    // If mismatch, another worker owns the job (reject completion)
}
```

**Lock Token Validation**:
- **Lock acquisition**: `SET bull:{queue}:{jobId}:lock {token} PX 30000`
- **Heartbeat**: `PEXPIRE bull:{queue}:{jobId}:lock 30000` only if GET returns matching token
- **Completion**: `DEL bull:{queue}:{jobId}:lock` only if GET returns matching token
- **All checks done in Lua scripts** (atomic, no race conditions)

**Performance Considerations**:
- **Generation cost**: ~1-2 microseconds per token (uses crypto/rand.Read)
- **Lock token not in hot path**: Generated once per job pickup (~10ms operation, token generation is 0.02% of pickup time)
- **No need for optimization**: Security > performance for lock tokens

**Token Format**:
- Standard UUID format: `xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx` (36 characters)
- Version 4 (indicated by `4` in 3rd group)
- Variant bits (indicated by `y` = 8, 9, a, or b)

**References**:
- [UUID v4 Specification (RFC 4122)](https://tools.ietf.org/html/rfc4122)
- [google/uuid Library](https://github.com/google/uuid)
- [Lock Hijacking Attack Vector (Academic Paper)](https://www.usenix.org/system/files/conference/usenixsecurity16/sec16_paper_sivakorn.pdf)

---

## 4. Heartbeat Timing Strategy

**Decision**:
- **Lock TTL**: 30 seconds
- **Heartbeat interval**: 15 seconds (50% of TTL)
- **Stalled check interval**: 30 seconds

**Rationale**:

**Lock TTL = 30s**:
- **Long enough**: Tolerates network hiccups (5-10s disruptions common in cloud environments)
- **Short enough**: Stalled jobs detected within 30-60s (acceptable recovery time for most applications)
- **Industry standard**: AWS SQS visibility timeout default is 30s, Azure Service Bus default is 30s
- **Production proven**: BullMQ Node.js default is 30s (13M+ downloads/month, battle-tested)

**Heartbeat Interval = 15s (50% of TTL)**:
- **50% rule**: Industry best practice (heartbeat at half of TTL)
- **One failure tolerated**: If one heartbeat fails, next succeeds before lock expires
- **Example timeline**:
  - T=0s: Job picked up, lock acquired (TTL=30s)
  - T=15s: Heartbeat #1 extends lock to T=45s
  - T=30s: Heartbeat #2 extends lock to T=60s (if #1 failed, lock still valid until T=30s)
  - T=45s: Heartbeat #3 extends lock to T=75s
- **Low overhead**: ~10ms per extension × 4 extensions/min = 40ms/min overhead per job
- **Failure scenario**: Heartbeat at T=15s fails, but T=30s succeeds before lock expires → job continues

**Stalled Check Interval = 30s**:
- **Detection latency**: Stalled jobs detected within 30-60s of lock expiry
- **Example timeline**:
  - T=0s: Job picked up (lock TTL=30s)
  - T=30s: Lock expires (worker crashed, heartbeat stopped)
  - T=30-60s: Next stalled check finds expired lock, requeues job
- **Performance**: Checking every 30s balances detection speed vs Redis load (scanning active jobs list)
- **Independent**: Stalled checker runs as separate goroutine, not tied to worker pool size

**Alternatives Considered**:

1. **Lock TTL: 10s, Heartbeat: 5s**
   - **Rejected**: Too aggressive, won't tolerate network disruptions
   - **Issue**: Single packet loss (10-20ms) can cascade to heartbeat failure (job stalls unnecessarily)
   - **Issue**: High Redis load (2x heartbeat operations)
   - **Issue**: 10s not enough for jobs that do external API calls (network latency spikes common)

2. **Lock TTL: 60s, Heartbeat: 30s**
   - **Rejected**: Slow recovery (60-120s to detect stalled jobs)
   - **Issue**: User-facing operations may timeout waiting for stalled job retry
   - **Trade-off**: More fault-tolerant but slower recovery (acceptable for batch processing, not for API-driven queues)

3. **Lock TTL: 30s, Heartbeat: 10s**
   - **Considered**: More aggressive heartbeat (3x failure tolerance)
   - **Rejected**: Unnecessary Redis load for marginal benefit
   - **Analysis**: 50% rule (15s) already tolerates 1 failure, sufficient for 99.9% of cases

4. **Lock TTL: 30s, Heartbeat: 20s**
   - **Rejected**: No failure tolerance (single heartbeat miss = stall)
   - **Issue**: Network latency of 11s would cause stall even if worker healthy
   - **Risk**: Too sensitive to network conditions

5. **Stalled Check: 5s**
   - **Rejected**: Excessive Redis load scanning active jobs list
   - **Issue**: For 1000 active jobs, checking every 5s = 200 checks/second (wasteful)
   - **Analysis**: 30s interval sufficient (jobs stalled 30-60s acceptable for most use cases)

6. **Stalled Check: 60s**
   - **Rejected**: Slow detection (60-120s to requeue stalled jobs)
   - **Issue**: User-facing operations timeout waiting for recovery

**Long Scan Handling** (when active jobs > 10,000):

**Problem**: Scanning 10,000+ active jobs takes > 100ms, potentially > 30s
- Lua script blocks Redis during execution (other commands queued)
- Overlapping cycles waste resources (two scanners running simultaneously)
- Could delay other operations (heartbeat extensions, job pickup)

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
- **Target**: < 100ms for 10,000 active jobs
- **If consistently exceeding**, consider:
  1. Cursor-based iteration (batch process in chunks, yield between batches)
  2. Increase check interval to 60s (reduce check frequency)
  3. Partition queue across multiple Redis instances (horizontal scaling)

**Implementation Notes**:

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

**Heartbeat Failure Handling**:

**Policy**: Continue processing despite heartbeat failures (no circuit breaker, no retry limit)

**Rationale**:
- Heartbeat failures are usually transient (network hiccup, Redis latency spike)
- Stopping job processing proactively wastes work already done (job may be 90% complete)
- Stalled checker provides safety net (requeues job if lock expires, no data loss)
- Idempotency requirement protects against duplicate processing (user's responsibility)

**Behavior on Heartbeat Failure**:
1. **Log error**: `logger.Warn("Heartbeat failed", "jobId", job.ID, "error", err)`
2. **Increment metric**: `bullmq_heartbeat_failure_total{queue="myqueue"}`
3. **Continue processing**: Worker does NOT abort job (no circuit breaker)
4. **No retry limit**: Heartbeat attempts every 15s until job completes or lock expires
5. **Lock expiration**: If 30s pass without successful heartbeat, lock expires
6. **Stalled detection**: Stalled checker requeues job within 30-60s
7. **Race condition**: Original worker may complete job after requeue (idempotency handles duplicate)

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
T=65s:   Different worker picks up job, processes again (idempotent handler prevents issues)
```

**NOT Implemented (by design)**:
- ❌ Circuit breaker (stop heartbeat after N consecutive failures)
- ❌ Exponential backoff for heartbeat retries (always 15s interval)
- ❌ Proactive job failure on heartbeat failure (let stalled checker handle)
- ❌ Lock ownership validation before completion (Lua script handles atomically)

**Monitoring & Alerts**:

**Metrics to track**:
- `bullmq_heartbeat_success_total{queue}` - Should grow linearly with active jobs
- `bullmq_heartbeat_failure_total{queue}` - Should be < 5% of success rate
- `bullmq_stalled_jobs_total{queue}` - Should be low (< 1% of completed jobs)
- `bullmq_lock_extend_duration_seconds{queue}` - Should be < 10ms (p95)
- `bullmq_stalled_checker_skipped_total` - Should be 0 (indicates large active list)

**Alerts**:
- Heartbeat failure rate > 5% → Investigate Redis latency, network issues
- Stalled jobs > 10% of active jobs → Investigate worker stability, OOM crashes
- Stalled checker skipped > 10% → Active list too large, consider partitioning

**Timing Guarantees**:
- **Max stall detection time**: 30s (lock expiry) + 30s (check interval) = 60s worst case
- **Typical detection time**: 30-45s (lock expiry + partial check interval)
- **Heartbeat failure tolerance**: 1 missed heartbeat tolerated before stall (15s grace period)

**References**:
- [BullMQ Worker Options (Node.js)](https://github.com/taskforcesh/bullmq/blob/master/src/interfaces/worker-options.ts)
- [AWS SQS Visibility Timeout](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-visibility-timeout.html)
- [Heartbeat Best Practices (Google SRE Book)](https://sre.google/sre-book/monitoring-distributed-systems/)

---

## 5. Error Categorization Strategy

**Decision**: Categorize errors as **transient** (retry with backoff) or **permanent** (fail immediately) using explicit error type checking and pattern matching.

**Rationale**:
- **User control**: Library categorizes common errors, but users can override via custom error types (flexible)
- **Fail fast**: Permanent errors (validation, auth) don't waste retry attempts (saves resources, faster user feedback)
- **Automatic retry**: Transient errors (network, Redis) automatically retried without user intervention (resilience)
- **Clear semantics**: Error category determines job fate (retry queue vs dead letter queue)
- **Resource efficiency**: Don't clog queue with unretryable jobs (validation errors will never succeed)

**Error Categories**:

**Transient Errors (Retry with Exponential Backoff)**:
- Network errors (connection refused, timeout, DNS failure, packet loss)
- Redis errors (connection loss, command timeout, LOADING, OOM)
- HTTP 5xx errors (500 Internal Server Error, 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout)
- Rate limit errors (429 Too Many Requests, Retry-After header)
- Temporary resource exhaustion (disk full, memory pressure, CPU throttling)
- Database connection errors (connection pool exhausted, deadlock)
- External service timeouts (API call timeout, webhook timeout)

**Permanent Errors (Fail Immediately)**:
- Validation errors (missing required fields, invalid data types, schema mismatch)
- HTTP 4xx errors (400 Bad Request, 401 Unauthorized, 403 Forbidden, 404 Not Found, 422 Unprocessable Entity)
- Authentication/authorization failures (invalid API key, expired token, insufficient permissions)
- Business logic errors (insufficient funds, duplicate transaction, invalid state transition)
- Parse errors (malformed JSON, invalid UTF-8, corrupt data)
- Configuration errors (invalid queue name, missing environment variables)

**Implementation Pattern**:

```go
// Error categorization
type ErrorCategory int

const (
    ErrorCategoryTransient ErrorCategory = iota
    ErrorCategoryPermanent
)

// Categorize error
func CategorizeError(err error) ErrorCategory {
    if err == nil {
        return ErrorCategoryPermanent // No error, shouldn't be categorizing
    }

    // Network errors → transient
    var netErr net.Error
    if errors.As(err, &netErr) {
        return ErrorCategoryTransient
    }

    // Context errors → transient (timeout, canceled)
    if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
        return ErrorCategoryTransient
    }

    // Redis errors
    if errors.Is(err, redis.Nil) {
        return ErrorCategoryPermanent // Key not found (job deleted intentionally)
    }
    if strings.Contains(err.Error(), "connection refused") {
        return ErrorCategoryTransient
    }
    if strings.Contains(err.Error(), "i/o timeout") {
        return ErrorCategoryTransient
    }
    if strings.Contains(err.Error(), "LOADING") {
        return ErrorCategoryTransient // Redis loading dataset
    }

    // HTTP errors
    var httpErr *HTTPError
    if errors.As(err, &httpErr) {
        if httpErr.StatusCode >= 500 {
            return ErrorCategoryTransient // 5xx → retry
        }
        if httpErr.StatusCode == 429 {
            return ErrorCategoryTransient // Rate limit → retry
        }
        return ErrorCategoryPermanent // 4xx → fail
    }

    // User-defined categorization
    var permanentErr *PermanentError
    if errors.As(err, &permanentErr) {
        return ErrorCategoryPermanent
    }

    var transientErr *TransientError
    if errors.As(err, &transientErr) {
        return ErrorCategoryTransient
    }

    // Default: treat as permanent (fail fast, avoid infinite retries)
    return ErrorCategoryPermanent
}

// User-defined error types
type PermanentError struct {
    Err error
}

func (e *PermanentError) Error() string {
    return fmt.Sprintf("permanent error: %v", e.Err)
}

func (e *PermanentError) Unwrap() error {
    return e.Err
}

type TransientError struct {
    Err error
}

func (e *TransientError) Error() string {
    return fmt.Sprintf("transient error: %v", e.Err)
}

func (e *TransientError) Unwrap() error {
    return e.Err
}
```

**User Override Example**:

```go
// Business logic error (permanent - fail immediately)
func processPayment(job *bullmq.Job) error {
    amount := job.Data["amount"].(float64)
    accountBalance := getAccountBalance(job.Data["userId"])

    if amount > accountBalance {
        return &bullmq.PermanentError{
            Err: fmt.Errorf("insufficient funds: need $%.2f, have $%.2f", amount, accountBalance),
        }
    }

    // Process payment
    return nil
}

// External service error (transient - force retry even if library categorizes as permanent)
func callExternalAPI(job *bullmq.Job) error {
    resp, err := http.Get("https://api.example.com/data")
    if err != nil {
        // Wrap as transient to force retry (override library categorization)
        return &bullmq.TransientError{Err: err}
    }

    if resp.StatusCode == 404 {
        // API endpoint doesn't exist yet (being deployed), treat as transient
        return &bullmq.TransientError{
            Err: fmt.Errorf("API endpoint not ready: %s", resp.Request.URL),
        }
    }

    // Process response
    return nil
}
```

**Alternatives Considered**:

1. **Retry all errors by default**
   - **Rejected**: Wastes retry attempts on validation errors (will never succeed)
   - **Issue**: Job with invalid payload retries 3 times before failing (wasted work, 30s+ delay for user feedback)
   - **Example**: Invalid JSON payload retries indefinitely, never succeeds, clogs queue

2. **Never retry errors (user must retry explicitly)**
   - **Rejected**: Network errors common in distributed systems, should auto-retry (resilience)
   - **Issue**: Every user reimplements retry logic, error-prone (inconsistent backoff, no exponential backoff)
   - **Example**: Redis timeout fails job immediately, user didn't expect transient failure (data loss)

3. **Error codes (integer constants like HTTP status codes)**
   - **Rejected**: Not idiomatic Go (errors should be types with behavior, not codes)
   - **Issue**: Hard to extend (users can't add custom error codes without library changes)
   - **Example**: `if err.Code() == ErrNetworkFailure { ... }` (not Go-like, more like C/Java)

4. **Separate error types (RetryableError, FatalError)**
   - **Considered**: Similar to chosen approach
   - **Rejected**: "Retryable" ambiguous (retryable how many times? with what backoff?)
   - **Chosen approach** is clearer: TransientError (temporary failure, will likely succeed on retry) vs PermanentError (will never succeed)

**Implementation Notes**:

- **Default policy**: Transient → retry with exponential backoff, Permanent → move to failed queue (DLQ)
- **Max retries**: Configurable via `WorkerOptions.MaxAttempts` (default: 3)
- **Backoff**: Exponential backoff with jitter for transient errors (see section 8)
- **DLQ (Dead Letter Queue)**: `bull:{queue}:failed` sorted set stores permanently failed jobs
- **Metrics**: Track `bullmq_jobs_failed_total{queue, reason="transient_exhausted|permanent"}`
- **Logs**: Include error category in failure logs: `logger.Error("Job failed", "jobId", id, "category", "permanent", "error", err)`

**Best Practices**:
- **Fail fast**: Use PermanentError for validation, schema errors (save retry attempts)
- **Retry sparingly**: Use TransientError only for truly transient failures (network, rate limits)
- **Log errors**: Always log error category for debugging (helps identify infrastructure vs application issues)
- **Monitor patterns**: Alert on high transient error rate (indicates service issues, Redis downtime)
- **Document categorization**: Document which errors are transient vs permanent in job processor docs

**References**:
- [AWS Error Handling Best Practices](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
- [Google SRE Book: Handling Overload](https://sre.google/sre-book/handling-overload/)
- [Go Error Handling](https://go.dev/blog/error-handling-and-go)

---

## 6. Testing Strategy

**Decision**: Use `github.com/testcontainers/testcontainers-go` for integration tests with real Redis instances.

**Rationale**:
- **Real Redis**: Tests against actual Redis, not mocks (catches Redis version incompatibilities, Lua script edge cases)
- **Isolation**: Each test gets fresh Redis instance (no cross-test pollution, deterministic results)
- **CI/CD friendly**: Testcontainers works in CI (Docker-in-Docker support, GitHub Actions compatible)
- **Reproducible**: Same Redis version across dev machines and CI (Dockerfile pinned to Redis 7.x)
- **Lua scripts**: Mock Redis can't execute Lua scripts correctly (complex logic, KEYS/ARGV arrays, return values)
- **Cluster testing**: Can spin up 3-node Redis Cluster to validate hash tags (critical for production)

**Test Pyramid**:

```
        /\
       /  \  Load Tests (3)
      /----\  - 1000+ jobs/second throughput
     /      \  - Memory leak detection (10,000 jobs)
    /--------\  - Goroutine leak detection
   /          \ Integration Tests (30)
  /            \  - Redis operations (testcontainers)
 /              \  - Lua script execution
/________________\  - Job lifecycle (pickup → complete)
                    - Multi-worker concurrency
                    - Redis Cluster compatibility

                    Unit Tests (100+)
                    - Pure functions (no Redis)
                    - Error categorization
                    - Key building
                    - Backoff calculation
                    - WorkerID generation
```

**Unit Tests (100+ tests)**:
- **Purpose**: Test pure functions in isolation (no external dependencies)
- **Speed**: < 1ms per test (total suite < 100ms)
- **Coverage**: >90% for pure functions
- **Run frequency**: On every commit (pre-commit hook + CI)

**Examples**:
- `TestCategorizeError_NetworkError` - Network error → transient
- `TestCategorizeError_ValidationError` - Validation error → permanent
- `TestBuildKey_WithHashTag` - Verify `bull:{queue}:wait` format
- `TestExponentialBackoff_Calculation` - Verify 1s → 2s → 4s → 8s progression
- `TestExponentialBackoff_MaxCap` - Verify cap at 1 hour
- `TestWorkerID_Generation` - Verify `{hostname}-{pid}-{random}` format
- `TestWorkerID_Uniqueness` - Generate 1000 IDs, no duplicates

**Integration Tests (30+ tests)**:
- **Purpose**: Test Redis operations using real Redis (testcontainers)
- **Speed**: ~100-500ms per test (includes container startup/teardown)
- **Coverage**: All Redis interactions, Lua scripts, job lifecycle
- **Run frequency**: On every commit (CI)

**Examples**:
- `TestWorker_PickupJob` - Single worker picks up job from wait queue
- `TestWorker_Heartbeat` - Lock extended correctly every 15s
- `TestWorker_HeartbeatFailure` - Job continues processing despite heartbeat failure
- `TestWorker_StalledDetection` - Job requeued after lock expiry
- `TestWorker_Concurrency` - 10 workers, 100 jobs, no duplicate processing
- `TestWorker_Retry` - Failed job retries with exponential backoff
- `TestWorker_MaxAttempts` - Job moves to failed queue after 3 attempts
- `TestQueue_Pause` - Worker stops picking jobs when queue paused
- `TestQueue_Resume` - Worker resumes picking jobs when queue resumed
- `TestRedisCluster_HashTags` - All queue keys hash to same slot
- `TestRedisCluster_LuaScripts` - Multi-key Lua scripts execute without CROSSSLOT errors
- `TestEdgeCases_UnicodeData` - Job with emoji/Unicode processed correctly
- `TestEdgeCases_LargePayload` - Job with 10MB payload rejected (validation)
- `TestRaceCondition_CompletionVsStalled` - Job completion races with stalled checker (only one wins)

**Compatibility Tests (5+ tests)**:
- **Purpose**: Validate Node.js BullMQ interoperability
- **Speed**: ~1-5s per test (includes Node.js process spawning)
- **Coverage**: Cross-language job processing, Redis state format
- **Run frequency**: On every PR (pre-merge gate)

**Examples**:
- `TestCompatibility_NodeProducerGoConsumer` - Node.js adds job, Go worker processes
- `TestCompatibility_GoProducerNodeConsumer` - Go adds job, Node.js worker processes
- `TestCompatibility_ShadowTest` - Node.js + Go workers process same queue concurrently (no conflicts)
- `TestCompatibility_EventStreamFormat` - Events stream format matches Node.js exactly
- `TestCompatibility_RedisStateFormat` - Redis state (keys, values, types) matches Node.js

**Load Tests (3+ tests)**:
- **Purpose**: Performance validation, memory/goroutine leak detection
- **Speed**: ~10-60s per test (processes thousands of jobs)
- **Coverage**: Throughput, latency, resource usage
- **Run frequency**: On demand (before release, performance regression testing)

**Examples**:
- `TestLoad_Throughput` - Process 10,000 jobs with 10 workers, measure jobs/second (target: ≥1000/s)
- `TestLoad_MemoryLeak` - Process 10,000 jobs, measure memory before/after (target: <100MB growth)
- `TestLoad_GoroutineLeak` - Process 10,000 jobs, measure goroutines before/after (target: <10 growth)

**Alternatives Considered**:

1. **Mock Redis (miniredis, redismock)**
   - **Rejected**: Can't execute Lua scripts correctly (complex logic, return values)
   - **Issue**: Mock Redis behavior diverges from real Redis (false positives, false negatives)
   - **Example**: Mock may not enforce CROSSSLOT errors (false sense of security)
   - **Use case**: Still useful for unit testing key building logic (no Redis operations)

2. **External Redis instance (docker-compose)**
   - **Rejected**: Not isolated (tests may conflict if run in parallel)
   - **Issue**: Developer must remember to start Redis before tests (friction)
   - **Issue**: CI requires extra setup (docker-compose installation, background Redis)

3. **Embedded Redis (no Docker)**
   - **Rejected**: No pure-Go Redis implementation exists (Redis is C)
   - **Issue**: Would need to bundle Redis binary (licensing issues, cross-compilation complexity)

4. **Manual testing only**
   - **Rejected**: Unsustainable for regression prevention (can't catch subtle bugs)
   - **Issue**: Human error, time-consuming, not repeatable

**Implementation Notes**:

**Testcontainers Setup**:
```go
import (
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/wait"
)

func setupRedis(t *testing.T) *redis.Client {
    ctx := context.Background()

    req := testcontainers.ContainerRequest{
        Image:        "redis:7-alpine",
        ExposedPorts: []string{"6379/tcp"},
        WaitingFor:   wait.ForLog("Ready to accept connections"),
    }

    container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: req,
        Started:          true,
    })
    require.NoError(t, err)

    // Cleanup after test
    t.Cleanup(func() {
        container.Terminate(ctx)
    })

    host, _ := container.Host(ctx)
    port, _ := container.MappedPort(ctx, "6379")

    client := redis.NewClient(&redis.Options{
        Addr: fmt.Sprintf("%s:%s", host, port.Port()),
    })

    return client
}

func TestWorker_PickupJob(t *testing.T) {
    client := setupRedis(t)
    worker := bullmq.NewWorker("test-queue", client, bullmq.WorkerOptions{})

    // Add job to queue
    queue := bullmq.NewQueue("test-queue", client)
    job, err := queue.Add("test-job", map[string]interface{}{"key": "value"}, bullmq.JobOptions{})
    require.NoError(t, err)

    // Worker picks up job
    processedJob := make(chan *bullmq.Job, 1)
    worker.Process(func(job *bullmq.Job) error {
        processedJob <- job
        return nil
    })

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    go worker.Start(ctx)

    // Assert job processed
    select {
    case j := <-processedJob:
        assert.Equal(t, job.ID, j.ID)
    case <-ctx.Done():
        t.Fatal("Job not processed within timeout")
    }
}
```

**Redis Cluster Setup** (for hash tag testing):
```go
func setupRedisCluster(t *testing.T) *redis.ClusterClient {
    // Spin up 3-node Redis Cluster using testcontainers
    // Configure cluster with redis-cli --cluster create
    // Return ClusterClient
    // Validate hash tags work correctly (no CROSSSLOT errors)
}
```

**Cross-Language Test Example**:
```bash
# tests/compatibility/test.sh
#!/bin/bash

# Start Redis
docker run -d --name redis-compat -p 6379:6379 redis:7-alpine

# Install Node.js dependencies
cd tests/compatibility/nodejs
npm install bullmq

# Node.js producer adds 100 jobs
node producer.js &

# Go worker processes jobs
cd ../../../
go run examples/worker/main.go &

# Wait for jobs to complete
sleep 10

# Verify: All 100 jobs completed, no duplicates
node verify.js

# Cleanup
docker stop redis-compat
docker rm redis-compat
```

**Coverage Target**: >80% for `pkg/bullmq/` package

**CI Configuration**:
```yaml
# .github/workflows/test.yml
name: Tests
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Run unit tests
        run: go test -v -race -coverprofile=coverage.out ./pkg/bullmq/...

      - name: Run integration tests
        run: go test -v -tags=integration ./tests/integration/...

      - name: Run compatibility tests
        run: |
          npm install -g bullmq
          bash tests/compatibility/test.sh

      - name: Upload coverage
        uses: codecov/codecov-action@v3
        with:
          files: ./coverage.out
```

**Best Practices**:
- Run tests in parallel (`t.Parallel()`) for speed (testcontainers supports parallel tests)
- Use table-driven tests for multiple cases (reduce duplication)
- Always cleanup containers (`t.Cleanup()`) to prevent resource leaks
- Test error paths (Redis errors, network failures, invalid payloads)
- Test edge cases (empty queue, very large jobs, Unicode/emoji data, null bytes)
- Use meaningful assertions (assert.Equal, assert.Contains, not just assert.NoError)

**References**:
- [testcontainers-go Documentation](https://golang.testcontainers.org/)
- [Go Testing Best Practices](https://go.dev/doc/tutorial/add-a-test)
- [Test Pyramid Concept](https://martinfowler.com/articles/practical-test-pyramid.html)

---

## 7. Observability Patterns

**Decision**:
- **Metrics**: Optional `pkg/bullmq/metrics` package (Prometheus-compatible, no forced dependency)
- **Logging**: Pluggable logger interface (compatible with zerolog, zap, logrus, slog)

**Rationale**:
- **No forced dependencies**: Users not forced to use Prometheus if they have different monitoring stack (Datadog, NewRelic, CloudWatch)
- **Flexibility**: Users can bring their own logger (zerolog, zap, logrus, slog, or custom)
- **Optional**: Metrics package only imported if user needs it (zero overhead if not used)
- **Standard**: Prometheus is industry standard for Go metrics (Kubernetes, Docker, most cloud platforms)
- **Production requirement**: Observability is mandatory per constitution (metrics + logs)

**Metrics Design**:

**Optional Import (No Forced Dependency)**:
```go
import (
    "github.com/Lokeyflow/bullmq-go/pkg/bullmq"
    "github.com/Lokeyflow/bullmq-go/pkg/bullmq/metrics" // Optional
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
    // Enable metrics (Prometheus)
    metricsCollector := metrics.NewPrometheusCollector()

    worker := bullmq.NewWorker("queue", client, bullmq.WorkerOptions{
        MetricsCollector: metricsCollector, // Optional, nil = no metrics
    })

    // Expose /metrics endpoint
    http.Handle("/metrics", promhttp.Handler())
    go http.ListenAndServe(":9090", nil)

    worker.Start(ctx)
}
```

**Required Metrics** (Prometheus format):
- `bullmq_jobs_processed_total{queue, status="completed|failed|retried"}` (Counter) - Total jobs processed by status
- `bullmq_job_duration_seconds{queue}` (Histogram) - Job processing duration (p50, p95, p99)
- `bullmq_queue_length{queue, state="wait|active|completed|failed"}` (Gauge) - Current queue lengths
- `bullmq_stalled_jobs_total{queue}` (Counter) - Total stalled jobs detected and requeued
- `bullmq_heartbeat_success_total{queue}` (Counter) - Successful lock extensions
- `bullmq_heartbeat_failure_total{queue}` (Counter) - Failed lock extensions
- `bullmq_active_workers{queue, worker_id}` (Gauge) - Active workers per queue
- `bullmq_redis_connection_status{status="connected|disconnected"}` (Gauge) - Redis connection status (1 = connected, 0 = disconnected)
- `bullmq_redis_reconnect_attempts_total` (Counter) - Total Redis reconnection attempts
- `bullmq_stalled_checker_skipped_total{queue}` (Counter) - Stalled checker cycles skipped (previous cycle still running)

**Logger Interface**:

```go
// Logger interface (users implement with their preferred logger)
type Logger interface {
    Debug(msg string, keysAndValues ...interface{})
    Info(msg string, keysAndValues ...interface{})
    Warn(msg string, keysAndValues ...interface{})
    Error(msg string, keysAndValues ...interface{})
}

// Worker accepts logger (optional, nil = no-op logger)
worker := bullmq.NewWorker("queue", client, bullmq.WorkerOptions{
    Logger: myLogger, // zerolog, zap, logrus, slog, or custom
})

// Default: no-op logger (silent, zero overhead)
```

**Logger Adapters (Provided)**:

```go
// Zerolog adapter
type ZerologAdapter struct {
    logger zerolog.Logger
}

func NewZerologAdapter(logger zerolog.Logger) *ZerologAdapter {
    return &ZerologAdapter{logger: logger}
}

func (l *ZerologAdapter) Info(msg string, keysAndValues ...interface{}) {
    l.logger.Info().Fields(keysAndValues).Msg(msg)
}

func (l *ZerologAdapter) Error(msg string, keysAndValues ...interface{}) {
    l.logger.Error().Fields(keysAndValues).Msg(msg)
}

// Zap adapter
type ZapAdapter struct {
    logger *zap.SugaredLogger
}

func NewZapAdapter(logger *zap.SugaredLogger) *ZapAdapter {
    return &ZapAdapter{logger: logger}
}

func (l *ZapAdapter) Info(msg string, keysAndValues ...interface{}) {
    l.logger.Infow(msg, keysAndValues...)
}

func (l *ZapAdapter) Error(msg string, keysAndValues ...interface{}) {
    l.logger.Errorw(msg, keysAndValues...)
}

// Standard library slog adapter
type SlogAdapter struct {
    logger *slog.Logger
}

func NewSlogAdapter(logger *slog.Logger) *SlogAdapter {
    return &SlogAdapter{logger: logger}
}

func (l *SlogAdapter) Info(msg string, keysAndValues ...interface{}) {
    l.logger.Info(msg, keysAndValues...)
}

func (l *SlogAdapter) Error(msg string, keysAndValues ...interface{}) {
    l.logger.Error(msg, keysAndValues...)
}
```

**Required Log Events**:
- Job picked up: `logger.Info("Job picked up", "jobId", job.ID, "workerId", worker.ID, "queue", queue)`
- Job completed: `logger.Info("Job completed", "jobId", job.ID, "workerId", worker.ID, "duration", duration, "result", result)`
- Job failed: `logger.Error("Job failed", "jobId", job.ID, "workerId", worker.ID, "error", err, "category", category, "attemptsMade", attemptsMade)`
- Job retried: `logger.Warn("Job retried", "jobId", job.ID, "workerId", worker.ID, "attemptsMade", attemptsMade, "delay", delay)`
- Job stalled: `logger.Warn("Job stalled", "jobId", job.ID, "lockExpired", lockExpiredAt)`
- Heartbeat failure: `logger.Warn("Heartbeat failed", "jobId", job.ID, "workerId", worker.ID, "error", err)`
- Redis disconnected: `logger.Error("Redis disconnected", "error", err)`
- Redis reconnected: `logger.Info("Redis reconnected", "attempt", attempt, "duration", duration)`

**WorkerID Inclusion** (Critical for Traceability):
- **All logs MUST include `worker_id` field** for tracing which worker processed which job
- **Format**: `{hostname}-{pid}-{random}` (e.g., `worker-node-1-12345-a1b2c3`)
- **Purpose**: Debug production issues (which worker crashed? which worker processed duplicate job?)
- **Example**: `logger.Info("Job completed", "jobId", "123", "workerId", "worker-1-9876-abc123", "duration", "2.5s")`

**Alternatives Considered**:

1. **Force Prometheus dependency (no optional package)**
   - **Rejected**: Some users don't use Prometheus (Datadog, NewRelic, CloudWatch)
   - **Issue**: Bloats library dependencies unnecessarily (forces prometheus/client_golang import)
   - **Issue**: Users with different monitoring stack must work around Prometheus types

2. **No metrics at all**
   - **Rejected**: Observability is mandatory per constitution (production requirement)
   - **Issue**: Users can't monitor job queue health (throughput, error rate, queue length)
   - **Issue**: Production debugging impossible without metrics

3. **Force specific logger (zerolog or zap)**
   - **Rejected**: Users may already use different logger (migration friction)
   - **Issue**: Forces dependency that may conflict with user's choice
   - **Issue**: Some users prefer standard library slog (Go 1.21+)

4. **Standard library log.Logger only**
   - **Rejected**: No structured logging support (no key-value pairs)
   - **Issue**: Can't filter by log level (all logs are same level)
   - **Issue**: Can't extract fields for log aggregation (Splunk, ELK, CloudWatch Insights)

5. **OpenTelemetry instead of Prometheus**
   - **Considered**: More vendor-neutral (supports Prometheus, Datadog, NewRelic, Jaeger)
   - **Rejected**: Less mature in Go ecosystem (as of 2025, still evolving)
   - **Future consideration**: May add OpenTelemetry support in v2.x if ecosystem matures

**Implementation Notes**:

**Metrics Package Structure**:
```
pkg/bullmq/metrics/
├── collector.go      # MetricsCollector interface
├── prometheus.go     # Prometheus implementation
├── noop.go           # No-op implementation (default)
└── metrics_test.go
```

**MetricsCollector Interface**:
```go
// pkg/bullmq/metrics/collector.go
type MetricsCollector interface {
    IncrementJobsProcessed(queue, status string)
    ObserveJobDuration(queue string, duration time.Duration)
    SetQueueLength(queue, state string, length int)
    IncrementStalledJobs(queue string)
    IncrementHeartbeatSuccess(queue string)
    IncrementHeartbeatFailure(queue string)
    SetActiveWorkers(queue, workerID string, active bool)
    SetRedisConnectionStatus(connected bool)
    IncrementRedisReconnectAttempts()
    IncrementStalledCheckerSkipped(queue string)
}
```

**No-op Default (Zero Overhead)**:
```go
// Default: metrics disabled
worker := bullmq.NewWorker("queue", client, bullmq.WorkerOptions{
    // No MetricsCollector specified → uses no-op
})

// No-op collector (does nothing, zero overhead)
type NoopCollector struct{}

func (n *NoopCollector) IncrementJobsProcessed(queue, status string) {}
func (n *NoopCollector) ObserveJobDuration(queue string, duration time.Duration) {}
func (n *NoopCollector) SetQueueLength(queue, state string, length int) {}
// ... all methods are no-ops
```

**Prometheus Collector Implementation**:
```go
// pkg/bullmq/metrics/prometheus.go
type PrometheusCollector struct {
    jobsProcessed    *prometheus.CounterVec
    jobDuration      *prometheus.HistogramVec
    queueLength      *prometheus.GaugeVec
    stalledJobs      *prometheus.CounterVec
    heartbeatSuccess *prometheus.CounterVec
    heartbeatFailure *prometheus.CounterVec
    activeWorkers    *prometheus.GaugeVec
    redisConnected   prometheus.Gauge
    reconnectAttempts prometheus.Counter
    stalledSkipped   *prometheus.CounterVec
}

func NewPrometheusCollector() *PrometheusCollector {
    c := &PrometheusCollector{
        jobsProcessed: prometheus.NewCounterVec(
            prometheus.CounterOpts{
                Name: "bullmq_jobs_processed_total",
                Help: "Total number of jobs processed by status",
            },
            []string{"queue", "status"},
        ),
        jobDuration: prometheus.NewHistogramVec(
            prometheus.HistogramOpts{
                Name:    "bullmq_job_duration_seconds",
                Help:    "Job processing duration in seconds",
                Buckets: prometheus.DefBuckets, // 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10
            },
            []string{"queue"},
        ),
        // ... register other metrics
    }

    // Register with default Prometheus registry
    prometheus.MustRegister(c.jobsProcessed)
    prometheus.MustRegister(c.jobDuration)
    // ... register other metrics

    return c
}

func (c *PrometheusCollector) IncrementJobsProcessed(queue, status string) {
    c.jobsProcessed.WithLabelValues(queue, status).Inc()
}

func (c *PrometheusCollector) ObserveJobDuration(queue string, duration time.Duration) {
    c.jobDuration.WithLabelValues(queue).Observe(duration.Seconds())
}
```

**Performance Considerations**:
- **No-op collector**: Zero overhead (compiler optimizes away empty function calls)
- **Prometheus collector**:
  - Counter increment: ~100ns (sync.Mutex lock)
  - Histogram observation: ~1-2µs (bucket lookup + counter increment)
  - Not in hot path: Metrics updated once per job (10ms+ job duration >> 2µs metrics overhead)

**Best Practices**:
- **Always include `worker_id` in logs** for traceability
- **Log at appropriate levels**: DEBUG for pickup, INFO for completion, ERROR for failures
- **Avoid logging in hot path**: Heartbeat, stalled check (use metrics instead)
- **Use metrics for quantitative data**: Counts, durations, queue lengths
- **Use logs for qualitative data**: Error messages, context, debugging info
- **Structure logs**: Use key-value pairs (not string interpolation) for log aggregation
- **Monitor key metrics**: Alert on high error rate, queue length growth, stalled jobs

**Example Grafana Dashboard** (Prometheus metrics):
- Panel 1: Job throughput (jobs/second) - `rate(bullmq_jobs_processed_total[5m])`
- Panel 2: Error rate - `rate(bullmq_jobs_processed_total{status="failed"}[5m])`
- Panel 3: Job duration (p50, p95, p99) - `histogram_quantile(0.95, bullmq_job_duration_seconds)`
- Panel 4: Queue length - `bullmq_queue_length{state="wait"}`
- Panel 5: Active workers - `bullmq_active_workers`
- Panel 6: Heartbeat failure rate - `rate(bullmq_heartbeat_failure_total[5m])`

**References**:
- [Prometheus Go Client](https://github.com/prometheus/client_golang)
- [Zerolog](https://github.com/rs/zerolog)
- [Zap](https://github.com/uber-go/zap)
- [Go slog Package](https://pkg.go.dev/log/slog)
- [Go Logging Best Practices](https://dave.cheney.net/2015/11/05/lets-talk-about-logging)

---

## 8. Exponential Backoff Implementation

**Decision**: Use exponential backoff with jitter for retry delays.

**Formula**:
```
baseDelay = initialDelay * 2^(attemptsMade - 1)
jitter = random(0.8, 1.2)  // ±20% jitter
delay = min(baseDelay * jitter, maxDelay)
```

**Default Parameters**:
- **Initial delay**: 1000ms (1 second)
- **Max delay**: 3600000ms (1 hour)
- **Backoff multiplier**: 2x
- **Jitter**: ±20%

**Rationale**:
- **Exponential growth**: Gives transient failures time to recover (network partition, Redis restart, external service downtime)
- **Jitter**: Prevents thundering herd (multiple workers retrying simultaneously, overloading recovering service)
- **Max cap**: Prevents unbounded delay (jobs don't wait days to retry, reasonable recovery time)
- **Industry standard**: AWS, Google Cloud, Stripe, Kubernetes all use exponential backoff with jitter

**Backoff Schedule**:

| Attempt | Base Delay | Jitter Range (±20%) | Actual Range |
|---------|------------|---------------------|--------------|
| 1       | 1s         | 0.8x - 1.2x        | 0.8s - 1.2s  |
| 2       | 2s         | 0.8x - 1.2x        | 1.6s - 2.4s  |
| 3       | 4s         | 0.8x - 1.2x        | 3.2s - 4.8s  |
| 4       | 8s         | 0.8x - 1.2x        | 6.4s - 9.6s  |
| 5       | 16s        | 0.8x - 1.2x        | 12.8s - 19.2s|
| 6       | 32s        | 0.8x - 1.2x        | 25.6s - 38.4s|
| 7       | 64s        | 0.8x - 1.2x        | 51.2s - 76.8s|
| 8       | 128s       | 0.8x - 1.2x        | 102s - 154s  |
| 9       | 256s       | 0.8x - 1.2x        | 205s - 307s  |
| 10      | 512s       | 0.8x - 1.2x        | 410s - 614s  |
| 11      | 1024s      | **capped**         | 2880s - 4320s (48-72 min) |

**Max Delay Cap (1 hour)**:
- **Problem without cap**: Attempt 11 = 17 minutes, Attempt 15 = 4.5 hours (unacceptable for production)
- **Solution with cap**: Attempt 11+ = 48-72 minutes (reasonable for transient failures)
- **Configurable**: Users can override via `JobOptions.Backoff.MaxDelay`

**Why Jitter?**

**Problem**: Without jitter, all workers retry at exact same time (thundering herd)

**Example Scenario Without Jitter**:
```
T=0s:   Redis goes down (maintenance, crash, network partition)
T=0s:   100 workers have active jobs, heartbeat fails
T=15s:  100 workers retry heartbeat simultaneously (exact same time)
T=15s:  Redis receives 100 requests in < 10ms
T=15s:  Redis overloaded, fails again (cascading failure)
T=30s:  100 workers retry again simultaneously
T=30s:  Redis overloaded again
...     Repeat forever (system never recovers)
```

**Solution With Jitter (±20%)**:
```
T=0s:   Redis goes down
T=15s:  Worker A retries at T=15.2s (jitter: +13%)
T=15s:  Worker B retries at T=15.7s (jitter: +47%)
T=15s:  Worker C retries at T=15.4s (jitter: +27%)
...     Retries spread over 12-18s window (no spike)
T=30s:  Retries spread over 24-36s window
Result: Redis recovers without overload (graceful recovery)
```

**Alternatives Considered**:

1. **Linear backoff (1s, 2s, 3s, 4s, 5s, ...)**
   - **Rejected**: Doesn't give transient failures time to recover (too aggressive)
   - **Example**: Network partition takes 30s to heal, linear backoff retries every 3-5s (wastes 10 attempts, hammers recovering service)

2. **Fixed delay (1s, 1s, 1s, ...)**
   - **Rejected**: Retries too aggressively during outages (hammers failing service)
   - **Example**: Redis down for 5 minutes, worker retries 300 times at 1s intervals (wasteful, prevents recovery)

3. **Pure exponential (no jitter)**
   - **Rejected**: Thundering herd problem (all workers retry simultaneously)
   - **Example**: 100 workers retry at exact T=15s, overload recovering Redis

4. **Full random jitter (0 to 2x base delay)**
   - **Considered**: Used by AWS SDK
   - **Rejected**: Too much variance (some jobs retry immediately, others wait long time)
   - **Chosen approach** (±20%) more predictable while still preventing thundering herd

5. **No max cap (unbounded exponential)**
   - **Rejected**: Jobs wait days to retry on later attempts (unacceptable)
   - **Example**: Attempt 20 = 2^19 * 1s = 524,288s = 6 days (absurd)

**Implementation**:

```go
// pkg/bullmq/retry.go

func CalculateBackoff(attemptsMade int, config BackoffConfig) time.Duration {
    if attemptsMade < 1 {
        return time.Duration(config.InitialDelay) * time.Millisecond
    }

    // Base delay with exponential growth: initialDelay * 2^(attemptsMade-1)
    baseDelay := config.InitialDelay
    exponent := attemptsMade - 1 // First retry (attempt 1) uses initialDelay
    delay := baseDelay * int64(math.Pow(2, float64(exponent)))

    // Apply jitter (±20%): random between 0.8 and 1.2
    jitter := 0.8 + 0.4*rand.Float64() // Range: 0.8 to 1.2
    delay = int64(float64(delay) * jitter)

    // Cap at max delay
    if delay > config.MaxDelay {
        delay = config.MaxDelay
    }

    return time.Duration(delay) * time.Millisecond
}

// Usage in retry logic
func (r *Retryer) Retry(ctx context.Context, job *Job, err error) error {
    if job.AttemptsMade >= r.maxAttempts {
        // Exhausted retries → move to failed queue (DLQ)
        return r.completer.Fail(ctx, job, err)
    }

    delay := CalculateBackoff(job.AttemptsMade+1, job.Opts.Backoff)

    // Schedule retry with delay (add to delayed queue)
    return r.scheduleRetry(ctx, job, delay)
}
```

**Configurable via JobOptions**:
```go
job, err := queue.Add("job-name", data, bullmq.JobOptions{
    Attempts: 5, // Max retry attempts
    Backoff: bullmq.BackoffConfig{
        Type:         "exponential", // "fixed" or "exponential"
        InitialDelay: 1000,           // 1 second
        MaxDelay:     3600000,        // 1 hour (cap)
    },
})
```

**Redis Connection Loss Backoff** (Separate from Job Retry):
```go
// Worker reconnection uses different backoff (faster recovery)
func calculateReconnectDelay(attempt int) time.Duration {
    // Initial: 100ms, Max: 30s
    initialDelay := 100 * time.Millisecond
    maxDelay := 30 * time.Second

    delay := initialDelay * time.Duration(math.Pow(2, float64(attempt)))
    if delay > maxDelay {
        delay = maxDelay
    }

    // Add jitter (±20%)
    jitter := 0.8 + 0.4*rand.Float64()
    return time.Duration(float64(delay) * jitter)
}

// Attempt 1:  ~100ms
// Attempt 5:  ~1.6s
// Attempt 10: ~30s (capped)
```

**Best Practices**:
- **Use exponential backoff for all transient errors** (network, Redis, rate limits)
- **Always include jitter** to prevent thundering herd (±20% typical)
- **Cap max delay at reasonable value** (1 hour typical, 5 minutes for latency-sensitive workloads)
- **Log retry attempts with delay**: `logger.Info("Retrying job", "jobId", id, "attemptsMade", attempts, "delay", delay)`
- **Monitor retry rate**: Alert if > 10% of jobs retry (indicates infrastructure issues)
- **Consider business impact**: Some jobs require faster retry (user-facing) vs slower (batch processing)

**Performance Considerations**:
- **Backoff calculation**: ~100ns (math.Pow is fast for small exponents)
- **Jitter randomness**: ~50ns (rand.Float64 uses pseudo-random generator)
- **Not in hot path**: Calculated once per retry (only for failed jobs)

**References**:
- [AWS Architecture Blog: Exponential Backoff and Jitter](https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/)
- [Google Cloud: Retry Strategy](https://cloud.google.com/storage/docs/retry-strategy)
- [Stripe API: Idempotency and Retries](https://stripe.com/docs/api/idempotent_requests)
- [Kubernetes Client-Go Backoff](https://pkg.go.dev/k8s.io/client-go/util/retry)

---

## Summary

This research document captures the key technology decisions for implementing the BullMQ Go client library. All decisions prioritize:

1. **Protocol compatibility** - Using official BullMQ Lua scripts (pinned to commit SHA v5.62.0)
2. **Production readiness** - Industry-standard patterns (exponential backoff, heartbeat, stalled detection)
3. **Performance** - go-redis/v9, connection pooling, script caching (EVALSHA)
4. **Security** - UUID v4 lock tokens (cryptographic randomness, 122 bits entropy)
5. **Testability** - testcontainers-go for real Redis testing (Lua scripts, cluster mode)
6. **Observability** - Optional metrics (no forced Prometheus), pluggable logger interface
7. **Resilience** - Error categorization, automatic retry, graceful degradation
8. **Flexibility** - Configurable timing parameters, backoff strategies, reconnection limits

These decisions form the foundation for implementation and align with the project constitution's core principles (Protocol Compatibility, Performance & Resource Efficiency, Operational Excellence, Error Handling & Resilience, Test-Driven Development, Observability & Monitoring, Resource Cleanup & Lifecycle Management, Public API Stability).

---

**Document Status**: Complete
**Next Step**: Proceed to Phase 1 (Foundation) implementation - data-model.md and contracts/ generation

**Blockers**: None
**Dependencies Resolved**: All technology decisions documented with rationale and alternatives considered
