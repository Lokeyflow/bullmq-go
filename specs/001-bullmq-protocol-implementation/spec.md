# Feature Specification: BullMQ Go Client Library

**Feature ID**: 001-bullmq-protocol-implementation
**Date**: 2025-10-29
**Status**: Draft
**Project Type**: Go Library (Open Source)

---

## Problem Statement

### Current State

Go developers needing to integrate with BullMQ-based job queues currently have limited options:

1. **No official Go client** - BullMQ is Node.js-only, no official Go support
2. **gobullmq is immature** - New library (August 2024, 14 stars), limited production testing, incomplete protocol support
3. **Manual Redis commands** - Error-prone, missing atomicity, race conditions, no protocol compliance
4. **Can't interoperate with Node.js** - Go services can't participate in existing BullMQ-based systems

### Impact

- **Integration barriers** - Go microservices can't consume jobs from Node.js producers
- **Protocol complexity** - Implementing BullMQ protocol manually is error-prone (Lua scripts, atomicity, edge cases)
- **Lack of reliability** - Manual implementations miss features like stalled job recovery, lock heartbeat, retry logic
- **Maintenance burden** - Custom implementations require ongoing maintenance as BullMQ evolves

---

## Solution Overview

Build a **production-ready BullMQ client library for Go** that provides full protocol compatibility with Node.js BullMQ.

### Approach

**Custom implementation** (not using gobullmq):
- Extract and port BullMQ's Lua scripts from official Node.js repository
- Implement Worker API for job consumption with heartbeat and stalled detection
- Implement Producer API for job submission with priority, delay, and scheduling
- Implement Queue Manager API for queue operations (pause, resume, clean, etc.)
- Support full BullMQ protocol (events, progress, logs, retries)
- Use Redis hash tags for cluster compatibility
- Comprehensive testing including cross-language compatibility tests

**Rationale for custom implementation**:
- gobullmq is too new (August 2024, 14 stars, limited production testing)
- BullMQ protocol is well-documented and stable
- Full control over implementation and optimization
- Can migrate to gobullmq later if it matures
- Learning opportunity for the Go community

---

## Functional Requirements

### FR-1: Job Consumption (Worker API)

**Requirement**: Library MUST provide Worker API to consume jobs from BullMQ queues.

**Acceptance Criteria**:
- [ ] Worker reads jobs from `bull:{queue}:prioritized` (ZSET) and `bull:{queue}:wait` (LIST)
- [ ] Worker respects job priority (higher priority = processed first)
- [ ] Worker respects delayed jobs (not processed before scheduled time)
- [ ] Worker respects paused queue state (no processing when paused)
- [ ] Worker respects rate limits (queue-wide rate limiting)
- [ ] Worker provides processor function interface for user-defined job handling
- [ ] Worker supports concurrent job processing (configurable concurrency)

**CRITICAL: Idempotency Requirement**:
- [ ] Documentation MUST state that job handlers MUST be idempotent
- [ ] **Rationale**: Jobs may be processed multiple times due to:
  - Worker crash during processing (job requeued by stalled checker)
  - Lock expiration during long processing (heartbeat failure)
  - Network partition causing duplicate pickup (rare but possible)
- [ ] **User Responsibility**: Implement idempotent handlers using:
  - Idempotency keys (check if operation already performed)
  - Database transactions with unique constraints
  - External system idempotency tokens (Stripe, PayPal, etc.)
- [ ] **Library Guarantee**: At-least-once delivery (NOT exactly-once)

**API Example**:
```go
worker := bullmq.NewWorker("myqueue", redisClient, bullmq.WorkerOptions{
    Concurrency: 10,
})

worker.Process(func(job *bullmq.Job) error {
    // User-defined job processing logic
    fmt.Printf("Processing job %s: %v\n", job.ID, job.Data)
    return nil
})

worker.Start(ctx)
```

### FR-2: Job State Management

**Requirement**: Worker MUST manage job state transitions atomically.

**Acceptance Criteria**:
- [ ] Job moves wait/prioritized → active atomically with lock acquisition
- [ ] Job acquires lock with unique token on pickup
- [ ] **Lock tokens use UUID v4 (cryptographically random)** for security
  - NOT UUID v1 (timestamp-based, predictable)
  - Prevents lock hijacking attacks
  - Generated via `github.com/google/uuid.NewRandom()`
- [ ] Job lock is extended via heartbeat every 15 seconds (configurable)
- [ ] Job moves active → completed/failed atomically on finish
- [ ] Job state transitions use BullMQ Lua scripts for atomicity
- [ ] Lock tokens prevent duplicate processing by multiple workers

**Heartbeat Failure Handling**:
- [ ] **Policy**: Continue processing job despite heartbeat failures (no circuit breaker)
- [ ] **Rationale**: Transient network issues shouldn't fail jobs immediately
- [ ] **Behavior on heartbeat failure**:
  1. Log error with metric increment (`bullmq_heartbeat_failure_total`)
  2. Continue job processing (don't abort proactively)
  3. If lock expires (30s without successful heartbeat), stalled checker requeues job
  4. Original worker may complete job after requeue (race condition, idempotency handles)
- [ ] **No retry limit**: Heartbeat attempts continue until job completes or lock expires
- [ ] **No circuit breaker**: Worker doesn't stop processing on consecutive heartbeat failures
- [ ] **Monitoring**: Alert on heartbeat failure rate > 5% (indicates Redis/network issues)

### FR-3: Stalled Job Recovery

**Requirement**: Worker MUST detect and recover stalled jobs (jobs whose lock expired).

**Acceptance Criteria**:
- [ ] Stalled checker runs every 30 seconds (configurable)
- [ ] Jobs with expired locks are requeued atomically
- [ ] Stalled jobs increment `attemptsMade` counter
- [ ] Stalled event emitted to events stream
- [ ] Stalled checker runs independently of worker pool

**Long Scan Handling** (when active jobs > 10,000):
- [ ] **Policy**: Skip overlapping cycles to prevent Redis blocking
- [ ] **Behavior**: If previous cycle still running, skip current cycle
- [ ] **Metric**: `bullmq_stalled_checker_skipped_total` counter
- [ ] **Alert threshold**: > 10% cycles skipped indicates large active list
- [ ] **Performance target**: Stalled check completes within 100ms for 10k jobs
- [ ] **Alternative for large queues**: Implement cursor-based iteration if needed

### FR-4: Retry Logic

**Requirement**: Worker MUST support configurable retry logic with exponential backoff.

**Acceptance Criteria**:
- [ ] User can configure max retry attempts (default: 3)
- [ ] User can configure backoff strategy (fixed or exponential)
- [ ] Exponential backoff: `min(delay * 2^(attemptsMade-1), maxDelay)`
- [ ] **Backoff cap**: Maximum delay is 3600000ms (1 hour) to prevent unbounded growth
  - Prevents 17-minute delays on attempt 11, 4.5-hour delays on attempt 15
  - Configurable via WorkerOptions.MaxBackoffDelay (default: 1 hour)
- [ ] Jobs exceeding max attempts move to failed queue (DLQ)
- [ ] Retry delay is configurable

### FR-5: Progress & Logs

**Requirement**: Worker MUST support progress reporting and log collection.

**Acceptance Criteria**:
- [ ] Worker provides API to update job progress (0-100)
- [ ] Progress updates stored in job hash (`progress` field)
- [ ] Progress events emitted to events stream
- [ ] Worker provides API to append log entries
- [ ] Log entries stored in job logs list (with trimming to prevent unbounded growth)

**API Example**:
```go
worker.Process(func(job *bullmq.Job) error {
    job.UpdateProgress(25)
    job.Log("Started processing")

    // ... processing ...

    job.UpdateProgress(75)
    job.Log("Almost done")

    return nil
})
```

### FR-6: Job Completion

**Requirement**: Worker MUST complete jobs with results or failure details.

**Acceptance Criteria**:
- [ ] Completed jobs move to `completed` sorted set with result data
- [ ] Failed jobs move to `failed` sorted set with error details
- [ ] Job hash stores `returnvalue` (success) or `failedReason` (failure)
- [ ] `removeOnComplete`/`removeOnFail` options respected
- [ ] Completion/failure events emitted to events stream

### FR-7: Job Production (Producer API)

**Requirement**: Library MUST provide Producer API to add jobs to queues.

**Acceptance Criteria**:
- [ ] Producer can add jobs with custom data payload
- [ ] Producer supports job priority (higher priority = processed first)
- [ ] Producer supports delayed jobs (scheduled for future processing)
- [ ] Producer supports job options (attempts, backoff, removeOnComplete, etc.)
- [ ] Producer validates job options before Redis write (fail fast on invalid input)
- [ ] Producer generates unique job IDs (or accepts user-provided IDs)
- [ ] Producer emits "waiting" event to events stream

**Validation Requirements**:
- [ ] Reject negative values: Priority < 0, Delay < 0, Attempts <= 0, Backoff.Delay <= 0
- [ ] Reject invalid backoff types (must be "fixed" or "exponential")
- [ ] Reject negative RemoveOnComplete/Fail values
- [ ] Warn on large RemoveOnComplete/Fail values (> 10000, performance impact)
- [ ] **Enforce max payload size**: Job data MUST be <= 10MB after JSON serialization
  - Redis string value limit is 512MB, but 10MB is practical limit for job queues
  - Validation MUST occur before Redis write (fail fast with clear error)
  - Error message MUST include actual size vs limit: "Job payload 12.3 MB exceeds limit of 10 MB"
  - Size check includes job.Data + job.Opts serialized together
- [ ] Validation errors MUST include specific field name and constraint violated

**API Example**:
```go
queue := bullmq.NewQueue("myqueue", redisClient)

job, err := queue.Add("job-name", map[string]interface{}{
    "userId": "123",
    "action": "send-email",
}, bullmq.JobOptions{
    Priority: 10,
    Delay: 5 * time.Second,
    Attempts: 3,
    Backoff: bullmq.BackoffConfig{
        Type: "exponential",
        Delay: 1000, // milliseconds
    },
})
```

### FR-8: Queue Management API

**Requirement**: Library MUST provide Queue API for queue operations.

**Acceptance Criteria**:
- [ ] Pause queue (stops job processing)
- [ ] Resume queue (resumes job processing)
- [ ] Clean queue (remove completed/failed jobs)
- [ ] Get job counts (waiting, active, completed, failed)
- [ ] Get job by ID
- [ ] Remove job by ID
- [ ] Drain queue (remove all jobs)

**API Example**:
```go
queue := bullmq.NewQueue("myqueue", redisClient)

// Pause/resume
queue.Pause()
queue.Resume()

// Clean old jobs
queue.Clean(24*time.Hour, 1000, "completed")

// Get counts
counts, _ := queue.GetJobCounts()
fmt.Printf("Waiting: %d, Active: %d\n", counts.Waiting, counts.Active)

// Get job
job, _ := queue.GetJob("job-123")
```

### FR-9: Redis Cluster Compatibility

**Requirement**: Library MUST use Redis hash tags for cluster compatibility.

**Acceptance Criteria**:
- [ ] All keys use pattern `bull:{queue-name}:*` with hash tags
- [ ] Job keys use pattern `bull:{queue-name}:{jobId}`
- [ ] Lock keys use pattern `bull:{queue-name}:{jobId}:lock`
- [ ] All keys for a queue land in same Redis Cluster slot
- [ ] Multi-key Lua scripts work correctly in cluster mode

**Testing Requirements**:
- [ ] Integration test validates all queue keys hash to same slot
- [ ] Integration test executes multi-key Lua scripts in Redis Cluster (no CROSSSLOT errors)
- [ ] Integration test uses testcontainers with 3-node Redis Cluster
- [ ] Negative test validates keys WITHOUT hash tags fail with CROSSSLOT error
- [ ] CI runs Redis Cluster tests on every build (not just single-node Redis)

### FR-10: Event Stream

**Requirement**: Library MUST emit events to Redis Stream for monitoring.

**Acceptance Criteria**:
- [ ] Events emitted to `bull:{queue}:events` stream
- [ ] Event types: waiting, active, progress, completed, failed, stalled, retry
- [ ] Events include jobId, timestamp, attemptsMade, and event-specific data
- [ ] Event format matches Node.js BullMQ for interoperability
- [ ] **Stream retention policy**: XADD with MAXLEN ~10000 (approximate trim)
  - Prevents unbounded stream growth (memory exhaustion)
  - Keeps approximately last 10,000 events per queue
  - Uses approximate trimming (~) for performance (O(1) vs O(N))
  - Configurable via WorkerOptions.EventsMaxLen (default: 10000)
- [ ] Events older than max length are automatically evicted (FIFO)

---

## Non-Functional Requirements

### NFR-1: Performance

**Requirement**: Library MUST meet performance targets.

**Targets**:
- Job pickup latency: < 10ms (moveToActive.lua)
- Lock heartbeat: < 10ms per extension
- Stalled check: < 100ms per cycle
- Library overhead: < 5% latency vs manual Redis commands
- Support 1000+ jobs/second per worker with 10 concurrent processors

**Validation**: Load testing with 10+ concurrent workers, 100+ jobs

### NFR-2: Reliability

**Requirement**: Library MUST handle failures gracefully.

**Targets**:
- No job loss on worker crash (jobs requeued by stalled checker)
- Graceful shutdown: wait for active jobs (configurable timeout, default 30s)
- Automatic reconnection on Redis connection loss
- Lock heartbeat survives temporary network issues

**Redis Connection Loss Handling**:
- [ ] **Retry Strategy**: Exponential backoff with jitter
  - Initial retry: 100ms
  - Max retry delay: 30s
  - Backoff multiplier: 2x
  - Jitter: ±20% to prevent thundering herd
- [ ] **Retry Limits**: Unlimited retries (configurable via WorkerOptions.MaxReconnectAttempts, default: 0 = infinite)
  - **Rationale**: Redis downtime is usually temporary, worker should survive restarts
  - **Override**: Set MaxReconnectAttempts > 0 to fail after N attempts
- [ ] **Backoff Formula**: `min(initialDelay * 2^attempt * (0.8 + 0.4*rand()), maxDelay)`
- [ ] **Behavior during disconnect**:
  1. Stop picking up new jobs
  2. Active jobs continue processing (use cached job data)
  3. Heartbeat failures logged (jobs may stall if disconnect > 30s)
  4. Reconnect in background
  5. Resume job pickup after successful reconnect
- [ ] **Metrics**: `bullmq_redis_reconnect_attempts_total`, `bullmq_redis_connection_status{status="connected|disconnected"}`
- [ ] **Max downtime tolerance**: Jobs stall after 30s (lock expiration), requeued by stalled checker within 60s

**Validation**: Chaos testing (worker crash, Redis restart, network partition)

### NFR-3: Observability

**Requirement**: Library MUST provide comprehensive observability.

**Metrics Required** (Prometheus format):
- Job counts: `bullmq_jobs_processed_total{queue, status="completed|failed|retried"}`
- Job durations: `bullmq_job_duration_seconds{queue}`
- Queue lengths: `bullmq_queue_length{queue, state="wait|active|completed|failed"}`
- Stalled jobs: `bullmq_stalled_jobs_total{queue}`
- Heartbeat: `bullmq_heartbeat_success_total{queue}`, `bullmq_heartbeat_failure_total{queue}`
- Worker ID: `bullmq_active_workers{queue, worker_id}` (gauge)

**Logs Required**:
- Structured logging (compatible with zerolog, zap, logrus)
- Log levels: DEBUG, INFO, WARN, ERROR
- Job lifecycle events (picked up, completed, failed, retried, stalled)
- Error categorization (transient vs permanent)
- **WorkerID included in all logs** for traceability

**WorkerID Generation**:
- **Format**: `{hostname}-{pid}-{random}` (e.g., `worker-node-1-12345-a1b2c3`)
- **Hostname**: os.Hostname() for deployment identification
- **PID**: os.Getpid() for process identification
- **Random**: 6-char hex for uniqueness across restarts
- **Uniqueness**: Not strictly enforced (used for observability only, not locking)
- **Configurable**: User can override via WorkerOptions.WorkerID if needed
- **Collision handling**: Not required (WorkerID is for metrics/logs, not business logic)

### NFR-4: Testability

**Requirement**: Implementation MUST follow TDD (tests written before code).

**Test Types Required**:
- Unit tests: Key builder, error categorization, backoff calculation
- Integration tests: Redis operations, Lua scripts, heartbeat, stalled checker
- **Redis Cluster tests**: Multi-key Lua script execution, hash tag validation (CRITICAL P0)
- Compatibility tests: Node.js BullMQ → Go worker (and reverse)
- Load tests: 10+ concurrent workers, 100+ jobs for memory leak detection
- **Edge case tests**: Unicode/emoji/null bytes, race conditions, eviction scenarios (P1)

**Coverage Target**: > 80% for core components

**Critical Test Files**:

1. **P0: `tests/integration/redis_cluster_test.go`**
   - Validates hash tags ensure all queue keys in same Redis Cluster slot
   - Validates multi-key Lua scripts execute without CROSSSLOT errors
   - Uses testcontainers to spin up actual Redis Cluster (3 masters)
   - Prevents production CROSSSLOT errors that would break job processing

2. **P1: `tests/integration/edge_cases_test.go`**
   - 13 test cases for Unicode, emoji, null bytes, control characters
   - Validates JSON round-trip encoding preserves data integrity
   - Tests invalid UTF-8 handling and XSS payload storage
   - Prevents data corruption in production job payloads

3. **P1: `tests/integration/race_condition_test.go`**
   - Tests job completion racing with stalled checker
   - Validates Lua script atomicity (only one completion wins)
   - Prevents duplicate job processing in edge case timing
   - 3 scenarios: completion first, stalled first, simultaneous attempts

4. **P1: `tests/integration/redis_eviction_test.go`**
   - Tests Redis maxmemory eviction with volatile-lru policy
   - Validates stalled checker recovers jobs with evicted locks
   - Documents recommended maxmemory policies (noeviction best)
   - Prevents job loss in production when Redis hits memory limits

### NFR-5: Cross-Language Compatibility

**Requirement**: Library MUST be fully compatible with Node.js BullMQ.

**Acceptance Criteria**:
- [ ] Node.js producer → Go worker: jobs processed correctly
- [ ] Go producer → Node.js worker: jobs processed correctly
- [ ] Shadow test: Node.js + Go workers process same queue concurrently without conflicts
- [ ] Redis state format matches Node.js BullMQ exactly
- [ ] Event stream format matches Node.js BullMQ

---

## Out of Scope (MVP)

Explicitly excluded features for initial implementation:

1. **Repeatable Jobs** (cron-like scheduling) - requires separate scheduler component
2. **Job Flows/Dependencies** (`waiting-children` state) - requires parent/child job tracking
3. **Job Groups** (advanced group rate limiting) - complex grouping logic
4. **Sandboxed Processors** (job isolation/containerization)
5. **Job Prioritization within Active Jobs** - BullMQ doesn't support this
6. **Built-in Job UI** - Users can use BullMQ Board or similar tools

**Future Consideration**: These features can be added in later versions if needed.

---

## Success Criteria

### Functional Validation

- [ ] Worker consumes jobs from Node.js BullMQ producer
- [ ] Node.js BullMQ worker consumes jobs from Go producer
- [ ] Shadow test: Node.js + Go workers process same queue concurrently without conflicts
- [ ] Lock heartbeat prevents stalled detection during long jobs (>60s)
- [ ] Stalled jobs requeued within 60 seconds of lock expiry
- [ ] Progress updates visible in Redis and events stream
- [ ] Retry logic works (exponential backoff, max attempts)

### Performance Validation

- [ ] Job pickup latency < 10ms (load test with 1000 jobs)
- [ ] No memory leaks (process 10,000+ jobs)
- [ ] Library overhead < 5% vs manual Redis commands

### Reliability Validation

- [ ] Worker recovers from Redis connection loss (reconnect + resume)
- [ ] Worker crash leaves jobs in requeue-able state (stalled checker handles)
- [ ] Graceful shutdown completes active jobs or requeues them

### Compatibility Validation

- [ ] Redis state matches Node.js BullMQ format (key inspection, Redis diff)
- [ ] All Redis keys use hash tags for cluster compatibility
- [ ] Events stream format matches Node.js BullMQ

---

## Dependencies

### Technical Dependencies

- **Go 1.21+** (generics, context improvements)
- **Redis 6.0+** (Lua script support)
- **Redis Client**: github.com/redis/go-redis/v9
- **UUID**: github.com/google/uuid (lock token generation)
- **Testing**: github.com/stretchr/testify, github.com/testcontainers/testcontainers-go

### External Dependencies

- **BullMQ Lua Scripts** - Extract from https://github.com/taskforcesh/bullmq/tree/master/src/scripts
  - **Version**: v5.62.0 (released 2025-10-28)
  - **Commit SHA**: `6a31e0aeab1311d7d089811ede7e11a98b6dd408`
  - **Rationale**: Pin to exact commit to prevent protocol drift and ensure reproducible builds
- **BullMQ Protocol Documentation** - https://docs.bullmq.io/

---

## Implementation Phases

### Phase 1: Foundation (2-3 days)

**Deliverables**:
- Project structure (pkg/bullmq/, examples/, tests/)
- Lua script extraction and loading
- Key builder with hash tag support
- Core data structures (Job, JobOptions, BackoffConfig, Event)
- Redis client wrapper with script caching

**Milestone**: Basic project structure ready, scripts loaded

### Phase 2: Worker Implementation (3-4 days)

**Deliverables**:
- Job reader using `moveToActive.lua`
- Lock heartbeat implementation
- Stalled job checker
- Job completion logic (success/failure)
- Retry logic with exponential backoff
- Progress and log APIs

**Milestone**: Worker can consume, process, and complete jobs

### Phase 3: Producer & Queue APIs (2-3 days)

**Deliverables**:
- Producer API for job submission
- Job options (priority, delay, attempts, backoff)
- Queue management API (pause, resume, clean, counts)
- Event emission to Redis streams

**Milestone**: Full producer and queue management functionality

### Phase 4: Testing & Compatibility (2-3 days)

**Deliverables**:
- Integration tests with testcontainers
- Cross-language compatibility tests (Node.js interop)
- Load tests (performance validation)
- Chaos tests (reliability validation)

**Milestone**: Production-ready with full testing

### Phase 5: Documentation & Polish (1-2 days)

**Deliverables**:
- README with usage examples
- API documentation (godoc)
- Examples (worker, producer, queue management)
- Contributing guide
- Performance benchmarks

**Milestone**: Open source release ready

---

## Risk Assessment

### High Risk 🔴

| Risk | Mitigation |
|------|-----------|
| BullMQ protocol complexity underestimated | Start with Lua script extraction, validate with Node.js shadow tests |
| Race conditions in job state management | Use atomic Lua scripts, comprehensive concurrency tests |
| Lua script version compatibility issues | Pin to exact BullMQ version (v5.62.0, commit `6a31e0a`), CI validates scripts match upstream, document compatibility matrix |

### Medium Risk 🟡

| Risk | Mitigation |
|------|-----------|
| Performance degradation vs Node.js | Load testing, profiling critical paths, optimize Lua script calls |
| Breaking changes in BullMQ protocol | Pin dependency version, monitor BullMQ releases, compatibility tests |
| Memory leaks in long-running workers | Memory profiling, load tests with 10,000+ jobs, proper goroutine cleanup |

### Low Risk 🟢

| Risk | Mitigation |
|------|-----------|
| Redis Cluster compatibility issues | Hash tags from day 1, cluster testing in CI |
| Insufficient documentation | Write docs alongside code, examples for all features |

---

## Open Questions

1. **Should we support BullMQ v4 and v5, or just v5?**
   - **Recommendation**: v5 only for MVP, add v4 if requested by users

2. **How to handle Logger interface?**
   - **Recommendation**: Accept standard `log.Logger` interface, provide adapters for popular loggers

3. **Should we provide Prometheus metrics by default or as optional module?**
   - **Recommendation**: Optional metrics package (`pkg/bullmq/metrics`) to avoid forcing dependencies

4. **Testing strategy for BullMQ integration?**
   - **Recommendation**: Both automated (testcontainers) + manual (Node.js validation scripts)

5. **Package name: `bullmq` or `bullmqgo`?**
   - **Recommendation**: `bullmq` (import as `github.com/username/bullmq-go/pkg/bullmq`)

---

## Approval

**Status**: ✅ Ready for Implementation
**Next Step**: Begin Phase 1 (Foundation)

---

**Document Status**: Ready for implementation
**Target Audience**: Library developers, Go community
**License**: MIT (or Apache 2.0, to be decided)
