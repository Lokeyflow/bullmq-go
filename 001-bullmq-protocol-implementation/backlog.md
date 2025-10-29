# Implementation Backlog: Remaining Issues

**Created**: 2025-10-29
**Status**: Post-P0/P1 QA Analysis
**Purpose**: Catalog remaining P2 issues for future implementation

---

## Overview

After completing all P0 (critical) and P1 (high priority) issues, the following P2 (medium priority) issues remain. These are **not blockers** for initial implementation but should be addressed before production release.

**Current Readiness Score**: ~92/100
**Target Score**: 95/100 (production-ready)

---

## P2 Issues by Category

### 🕐 **Timing & Scheduling (4 issues)**

#### P2-1: Delayed Job Polling Mechanism Undefined

**Issue**: Spec says delayed jobs moved "when ready" but mechanism is vague

**Current State**:
- `bull:{queue}:delayed` ZSET exists (score = timestamp)
- No polling frequency specified
- No latency SLO defined

**Proposed Solution**:
```yaml
mechanism: Separate goroutine polls delayed ZSET every 5s
query: ZRANGEBYSCORE delayed -inf {now} LIMIT 10
latency_slo: Delayed jobs picked up within 10s of scheduled time (P95)
performance: Batch move up to 10 jobs per cycle to avoid blocking
alternative: Integrate into moveToActive.lua (check delayed before wait)
```

**Impact**: Medium - Delayed jobs are non-critical feature
**Effort**: 2-3 hours
**Files to Update**: `spec.md`, `research.md`, `worker.go` (future)

---

#### P2-2: System Clock Jump Backward Handling

**Issue**: NTP correction or DST bugs could cause negative time deltas

**Current State**:
- Uses `time.Now().UnixMilli()` for timestamps
- No monotonic clock for delays
- Could cause negative backoff delays

**Proposed Solution**:
```yaml
timestamps: Continue using wall clock (required for absolute scheduling)
delays: Use time.Since() for relative delays (monotonic clock)
validation: Reject jobs with timestamp > now + 1 hour (clock skew tolerance)
heartbeat: Lock TTL uses Redis TIME command (server clock)
testing: Mock time.Now() to return decreasing values, verify no panics
```

**Impact**: Low - Rare occurrence, minimal user impact
**Effort**: 1-2 hours
**Files to Update**: `spec.md`, `research.md`, test file

---

#### P2-3: Graceful Shutdown Timeout Handling

**Issue**: What happens to jobs exceeding 30s shutdown timeout?

**Current State**:
- Spec says "wait for active jobs with 30s timeout"
- Behavior after timeout is undefined

**Proposed Solution**:
```yaml
policy: Jobs exceeding timeout are ABANDONED (not failed)
behavior:
  - Worker stops heartbeat immediately
  - Lock expires after 30s (total 60s max)
  - Stalled checker requeues job within 90s
  - Original worker exits without updating job state
rationale: Don't fail jobs that may still succeed (idempotency handles reprocess)
configuration: WorkerOptions.ShutdownTimeout (default 30s)
logging: WARN log for each abandoned job with jobId
metric: bullmq_shutdown_abandoned_jobs_total counter
```

**Impact**: Medium - Affects graceful deployments
**Effort**: 1-2 hours
**Files to Update**: `spec.md`, `research.md`

---

#### P2-4: Exponential Backoff Jitter for Job Retries

**Issue**: All jobs retry at exact same time (thundering herd)

**Current State**:
- Exponential backoff: `delay * 2^(attempt-1)`
- No jitter, jobs with same attempt retry simultaneously

**Proposed Solution**:
```yaml
formula: delay * 2^(attempt-1) * (0.9 + 0.2*rand())
jitter_range: ±10% to spread retries
example:
  - Base delay 1000ms, attempt 3 = 4000ms
  - With jitter: 3600-4400ms (±10%)
rationale: Prevents all failed jobs hitting external service at once
configuration: WorkerOptions.BackoffJitter (default 0.1 = 10%)
```

**Impact**: Low - Nice-to-have for high-volume queues
**Effort**: 1 hour
**Files to Update**: `data-model.md`, `research.md`

---

### 🔒 **Security & Validation (3 issues)**

#### P2-5: Redis AUTH Password Sanitization

**Issue**: Password may leak in debug logs or error messages

**Current State**:
- Redis connection string contains password
- No explicit sanitization documented

**Proposed Solution**:
```yaml
sanitization:
  - Redact password in all log messages
  - Error messages show: "redis://user:***@host:6379/0"
  - Never log full connection string
implementation:
  - Wrapper function: sanitizeRedisURL(connStr)
  - Apply in all logging statements
testing:
  - Trigger auth error, verify password not in logs
  - grep -r "password" in log output
```

**Impact**: High - Security best practice
**Effort**: 1-2 hours
**Files to Update**: `spec.md`, logging utilities

---

#### P2-6: Job Data PII Redaction in Logs

**Issue**: Job data may contain PII (email, SSN) logged in errors

**Current State**:
- Spec says "log job ID only" but not enforced

**Proposed Solution**:
```yaml
policy: NEVER log job.Data in default log level
default: Log jobId, jobName, attemptsMade only
opt_in: WorkerOptions.LogJobData = true for debugging
sanitization:
  - Redact known PII fields: email, ssn, creditCard, phone
  - Use regex patterns: \b[\w\.-]+@[\w\.-]+\.\w{2,4}\b
warning: Log "Job data contains N fields (set LogJobData=true to see)"
compliance: GDPR, CCPA compliant by default
```

**Impact**: High - Privacy/compliance requirement
**Effort**: 2-3 hours
**Files to Update**: `spec.md`, `CLAUDE.md`, logging

---

#### P2-7: Job Payload Circular Reference Detection

**Issue**: Circular refs in job.Data cause JSON serialization to hang

**Current State**:
- No validation for circular references
- `json.Marshal()` may infinite loop

**Proposed Solution**:
```yaml
validation: Check for circular refs before JSON serialization
detection: Track visited objects during traversal
error_message: "Job data contains circular reference at path: $.user.manager.subordinates[0]"
alternative: Use json.Marshal() timeout (context.WithTimeout)
performance: O(N) traversal, negligible for most payloads
```

**Impact**: Medium - Rare but causes worker hang
**Effort**: 2-3 hours
**Files to Update**: `spec.md`, `data-model.md`, validation code

---

### 📊 **Performance & Scalability (3 issues)**

#### P2-8: Worker Concurrency Limits & Validation

**Issue**: No documented max concurrency, users may set 10,000

**Current State**:
- Concurrency configurable, no upper bound
- 10,000 goroutines = memory/connection exhaustion

**Proposed Solution**:
```yaml
recommended: 10-100 (documented in CLAUDE.md)
warning_threshold: Concurrency > 100 → log WARN
error_threshold: Concurrency > 1000 → return validation error
rationale:
  - 1000 goroutines = 1000 concurrent jobs = 1000 Redis connections
  - Most use cases need 10-50 concurrency
  - Higher concurrency = horizontal scaling (more workers)
configuration: WorkerOptions.MaxConcurrency (default unlimited, opt-in limit)
```

**Impact**: Low - Documentation + validation
**Effort**: 1 hour
**Files to Update**: `spec.md`, `CLAUDE.md`, `data-model.md`

---

#### P2-9: Redis Connection Pool Tuning

**Issue**: No documented connection pool size or tuning

**Current State**:
- Uses go-redis default pool settings
- No recommendations for production

**Proposed Solution**:
```yaml
pool_size: 10 * Concurrency (default go-redis formula)
min_idle_conns: 5 (keep connections warm)
max_conn_age: 30 minutes (recycle to prevent stale connections)
pool_timeout: 4 seconds (fail fast on exhaustion)
configuration: Expose via WorkerOptions.RedisPoolOptions
documentation: Add to CLAUDE.md "Production Configuration" section
```

**Impact**: Low - go-redis defaults usually sufficient
**Effort**: 1-2 hours
**Files to Update**: `CLAUDE.md`, configuration

---

#### P2-10: Stalled Checker Cursor-Based Iteration

**Issue**: Scanning 100,000 active jobs blocks Redis

**Current State**:
- LRANGE fetches entire active list
- Performance target: < 100ms for 10k jobs
- No solution for 100k+ jobs

**Proposed Solution**:
```yaml
implementation: Cursor-based iteration in Lua script
algorithm:
  - Store cursor in Redis: bull:{queue}:stalled-cursor
  - Each cycle: LRANGE active {cursor} {cursor+1000}
  - Process 1000 jobs, update cursor
  - When cursor reaches end, reset to 0
latency: Constant O(1000) instead of O(N)
trade_off: Full scan takes multiple cycles (acceptable)
configuration: WorkerOptions.StalledCheckBatchSize (default 1000)
```

**Impact**: Low - Only needed for queues with 10k+ active jobs
**Effort**: 3-4 hours (Lua script modification)
**Files to Update**: `spec.md`, `research.md`, Lua script

---

### 🧪 **Testing & Validation (3 issues)**

#### P2-11: Mutation Testing Requirement

**Issue**: Code coverage doesn't prove tests actually validate behavior

**Current State**:
- Target: 80% line coverage
- No mutation testing requirement

**Proposed Solution**:
```yaml
tool: github.com/zimmski/go-mutesting
target: 70% mutation score
process:
  - Inject mutations (flip conditions, remove lines)
  - Run test suite
  - Measure % of mutations caught
ci_gate: Mutation score < 70% fails CI (warning only initially)
files_to_mutate:
  - pkg/bullmq/errors.go (error categorization)
  - pkg/bullmq/retry.go (backoff calculation)
  - pkg/bullmq/keys.go (hash tag generation)
```

**Impact**: Medium - Improves test quality
**Effort**: 4-5 hours (setup + fix failing tests)
**Files to Update**: `spec.md`, CI config

---

#### P2-12: Load Test Memory Leak Detection

**Issue**: 24-hour load test not defined

**Current State**:
- Spec mentions "10,000+ jobs" but no long-running test
- Memory leak detection mechanism undefined

**Proposed Solution**:
```yaml
test_duration: 24 hours
job_rate: 100 jobs/second = 8.6M jobs total
workers: 10 concurrent workers
metrics:
  - RSS memory growth: < 10MB/hour
  - Goroutine count: stable (not increasing)
  - Heap allocations: < 1% growth/hour
tooling:
  - pprof heap snapshots every hour
  - runtime.ReadMemStats() logged
failure_criteria: Memory growth > 100MB/24h or goroutines > 1000
```

**Impact**: High - Critical for production confidence
**Effort**: 6-8 hours (test setup + debugging)
**Files to Update**: `spec.md`, `tests/load/` directory

---

#### P2-13: Compatibility Test Automation

**Issue**: Node.js compatibility tests are manual

**Current State**:
- Spec describes shadow testing
- No automated CI integration

**Proposed Solution**:
```yaml
ci_setup:
  - Install Node.js 18+ in CI
  - npm install bullmq@5.62.0
  - Run Go producer → Node.js consumer test
  - Run Node.js producer → Go consumer test
validation:
  - 100 jobs submitted
  - 100% processed without errors
  - Redis state diff shows identical format
automation: tests/compatibility/run.sh script
```

**Impact**: High - Prevents protocol drift
**Effort**: 3-4 hours
**Files to Update**: `spec.md`, CI config, test scripts

---

## P2 Summary Table

| ID | Issue | Category | Impact | Effort | Priority |
|----|-------|----------|--------|--------|----------|
| P2-1 | Delayed job polling | Timing | Medium | 2-3h | 🟡 |
| P2-2 | Clock jump backward | Timing | Low | 1-2h | 🟢 |
| P2-3 | Shutdown timeout | Timing | Medium | 1-2h | 🟡 |
| P2-4 | Retry jitter | Timing | Low | 1h | 🟢 |
| P2-5 | Password sanitization | Security | High | 1-2h | 🔴 |
| P2-6 | PII redaction | Security | High | 2-3h | 🔴 |
| P2-7 | Circular refs | Security | Medium | 2-3h | 🟡 |
| P2-8 | Concurrency limits | Performance | Low | 1h | 🟢 |
| P2-9 | Connection pool | Performance | Low | 1-2h | 🟢 |
| P2-10 | Stalled cursor | Performance | Low | 3-4h | 🟢 |
| P2-11 | Mutation testing | Testing | Medium | 4-5h | 🟡 |
| P2-12 | Load test 24h | Testing | High | 6-8h | 🔴 |
| P2-13 | Compat automation | Testing | High | 3-4h | 🔴 |

**Total Estimated Effort**: 30-40 hours

---

## Recommended Implementation Order

### Phase 1: Security (High Impact, Quick Wins)
**Effort**: 6-8 hours
**Items**: P2-5, P2-6

These are critical for production compliance and relatively quick to implement.

### Phase 2: Testing Infrastructure (High Impact)
**Effort**: 13-17 hours
**Items**: P2-12, P2-13, P2-11

Establishes confidence for production deployment.

### Phase 3: Production Hardening (Medium Impact)
**Effort**: 6-8 hours
**Items**: P2-1, P2-3, P2-7

Improves production reliability and edge case handling.

### Phase 4: Performance Optimization (Low Impact)
**Effort**: 5-7 hours
**Items**: P2-4, P2-8, P2-9, P2-10

Nice-to-have improvements for high-scale scenarios.

### Phase 5: Edge Cases (Low Priority)
**Effort**: 1-2 hours
**Items**: P2-2

Handle rare scenarios (can defer to post-launch).

---

## Decision: When to Address P2 Issues?

### Option 1: Address Before Initial Release ✅ **Recommended**
- **Pro**: Production-ready from day 1
- **Pro**: Avoids technical debt
- **Pro**: Security/compliance handled upfront
- **Con**: Delays initial release by 1-2 weeks
- **Total Effort**: 30-40 hours

### Option 2: Ship Now, Fix in v0.2
- **Pro**: Faster initial release
- **Pro**: Get user feedback early
- **Con**: Security issues (P2-5, P2-6) are risks
- **Con**: Load test (P2-12) gap means unproven at scale
- **Must Do Before v1.0**: P2-5, P2-6, P2-12, P2-13

### Option 3: Hybrid Approach ⚡ **Pragmatic**
**Ship v0.1 with Phase 1 + Phase 2** (Security + Testing)
- Effort: 19-25 hours (1 week)
- Production-ready for low-to-medium scale
- Defer Phase 3-5 to v0.2 based on user feedback

---

## Tracking

**Status**: 📋 Backlog
**Next Review**: After Phase 1 (Foundation) implementation complete
**Owner**: TBD
**Target Completion**: Before v1.0 production release

---

## Notes

- This backlog assumes TDD approach (tests before implementation)
- Effort estimates include test writing + documentation updates
- Priority (🔴🟡🟢) based on production impact, not implementation order
- All issues have detailed proposed solutions to accelerate future work

**Last Updated**: 2025-10-29
