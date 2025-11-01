# Lua Scripts - BullMQ Protocol

**Version**: BullMQ v5.62.0 (commit `6a31e0aeab1311d7d089811ede7e11a98b6dd408`)
**Date**: 2025-10-30
**Purpose**: Document all Lua scripts used by BullMQ for atomic Redis operations

---

## Overview

BullMQ uses Lua scripts for all multi-key atomic operations to ensure correctness and prevent race conditions. Lua scripts execute atomically in Redis, providing transaction-like guarantees without MULTI/EXEC complexity.

**Why Lua Scripts?**
- **Atomicity**: All operations execute as single atomic unit (no interleaving)
- **Correctness**: Prevent race conditions (e.g., two workers picking same job)
- **Performance**: Single network round-trip vs multiple commands
- **Cluster Compatibility**: Work with Redis Cluster (when keys have same hash tag)

---

## Script Execution Model

### Loading Scripts
```go
// Scripts are loaded once and cached by SHA1
sha, err := redis.ScriptLoad(ctx, scriptContent).Result()
// Store SHA for later use

// Execute via EVALSHA (faster than EVAL)
result, err := redis.EvalSha(ctx, sha, keys, args).Result()
```

### Error Handling
```lua
-- Return errors with redis.error_reply()
if not lockToken then
    return redis.error_reply("Missing lock token")
end

-- Return success data
return {jobId, lockToken, jobData}
```

---

## Core Scripts

### 1. moveToActive.lua

**Purpose**: Atomically move job from wait/prioritized queue to active list and acquire lock

**Signature**:
```lua
EVALSHA <sha> 3 <wait-key> <active-key> <lock-key> <timestamp> <workerID> <lockDuration>
```

**KEYS**:
- `KEYS[1]`: `bull:{queue}:wait` (LIST) or `bull:{queue}:prioritized` (ZSET)
- `KEYS[2]`: `bull:{queue}:active` (LIST)
- `KEYS[3]`: `bull:{queue}:{jobId}:lock` (STRING with TTL)

**ARGV**:
- `ARGV[1]`: Current timestamp (Unix milliseconds, stringified)
- `ARGV[2]`: Worker ID (e.g., `worker-node-1-12345-a1b2c3`)
- `ARGV[3]`: Lock duration in seconds (e.g., `30`)

**Algorithm**:
1. Check if queue is paused (`HGET bull:{queue}:meta paused`)
   - If paused, return `nil`
2. Move delayed jobs to wait queue (if scheduled time reached)
3. Try prioritized queue first:
   - `ZPOPMAX bull:{queue}:prioritized 1` (highest priority)
   - If empty, fall back to wait queue
4. Try wait queue:
   - `LPOP bull:{queue}:wait` (FIFO)
5. If no job available, return `nil`
6. Acquire lock:
   - `SET bull:{queue}:{jobId}:lock {lockToken} EX {lockDuration} NX`
   - Lock token: UUID v4 (e.g., `a1b2c3d4-e5f6-7890-abcd-ef1234567890`)
7. Update job hash:
   - `HSET bull:{queue}:{jobId} processedOn {timestamp}`
8. Add to active list:
   - `RPUSH bull:{queue}:active {jobId}`
9. Return: `{jobId, lockToken, jobData}`

**Return Values**:
- **Success**: `[jobId, lockToken, jobData]` (array of 3 strings)
- **No job available**: `nil`
- **Queue paused**: `nil`

**Example**:
```redis
EVALSHA <sha> 3 "bull:{myqueue}:wait" "bull:{myqueue}:active" "bull:{myqueue}:1:lock" "1698765000000" "worker-1" "30"
# Returns: ["1", "a1b2c3d4-...", "{\"name\":\"send-email\",\"data\":\"...\"}"]
```

**Atomicity Guarantees**:
- Only one worker can acquire lock for a job
- Job cannot be picked up twice simultaneously
- Lock acquisition and active list insertion are atomic

---

### 2. moveToCompleted.lua

**Purpose**: Atomically mark job as completed, release lock, and optionally remove job data

**Signature**:
```lua
EVALSHA <sha> 3 <job-key> <lock-key> <completed-key> <lockToken> <returnvalue> <timestamp> <removeOnComplete>
```

**KEYS**:
- `KEYS[1]`: `bull:{queue}:{jobId}` (HASH)
- `KEYS[2]`: `bull:{queue}:{jobId}:lock` (STRING)
- `KEYS[3]`: `bull:{queue}:completed` (ZSET)

**ARGV**:
- `ARGV[1]`: Lock token (must match current lock value)
- `ARGV[2]`: Return value (JSON string, e.g., `{"sent":true}`)
- `ARGV[3]`: Current timestamp (Unix milliseconds)
- `ARGV[4]`: Remove on complete (boolean: `"1"` = remove, `"0"` = keep, or integer = keep last N)

**Algorithm**:
1. Verify lock ownership:
   - `GET bull:{queue}:{jobId}:lock`
   - If doesn't match `ARGV[1]`, return error (lock hijacking prevention)
2. Remove from active list:
   - `LREM bull:{queue}:active 1 {jobId}`
3. Update job hash:
   - `HSET bull:{queue}:{jobId} finishedOn {timestamp} returnvalue {returnvalue}`
4. Add to completed set:
   - `ZADD bull:{queue}:completed {timestamp} {jobId}`
5. Release lock:
   - `DEL bull:{queue}:{jobId}:lock`
6. Handle retention policy:
   - If `removeOnComplete == "1"`: Delete job hash immediately
   - If `removeOnComplete > 1`: Keep last N jobs, remove older ones via `ZREMRANGEBYRANK`
   - If `removeOnComplete == "0"`: Keep indefinitely
7. Return: `"1"` (success)

**Return Values**:
- **Success**: `1`
- **Lock mismatch**: `redis.error_reply("Lock token mismatch")`
- **Job not found**: `redis.error_reply("Job not found")`

**Example**:
```redis
EVALSHA <sha> 3 "bull:{myqueue}:1" "bull:{myqueue}:1:lock" "bull:{myqueue}:completed" "a1b2c3d4-..." "{\"sent\":true}" "1698765005000" "1000"
# Returns: 1
```

**Atomicity Guarantees**:
- Job cannot be completed twice (lock token verification)
- Active list removal and completed set addition are atomic
- No orphaned jobs in active list

---

### 3. moveToFailed.lua

**Purpose**: Atomically mark job as failed, release lock, and optionally schedule retry

**Signature**:
```lua
EVALSHA <sha> 4 <job-key> <lock-key> <failed-key> <wait-key> <lockToken> <failedReason> <stacktrace> <timestamp> <attemptsMade> <maxAttempts> <removeOnFail>
```

**KEYS**:
- `KEYS[1]`: `bull:{queue}:{jobId}` (HASH)
- `KEYS[2]`: `bull:{queue}:{jobId}:lock` (STRING)
- `KEYS[3]`: `bull:{queue}:failed` (ZSET)
- `KEYS[4]`: `bull:{queue}:wait` (LIST) - for retry

**ARGV**:
- `ARGV[1]`: Lock token
- `ARGV[2]`: Failed reason (error message string)
- `ARGV[3]`: Stack trace (JSON array of strings)
- `ARGV[4]`: Current timestamp (Unix milliseconds)
- `ARGV[5]`: Attempts made (stringified integer)
- `ARGV[6]`: Max attempts (stringified integer)
- `ARGV[7]`: Remove on fail (boolean or integer)

**Algorithm**:
1. Verify lock ownership (same as moveToCompleted)
2. Remove from active list
3. Check if should retry:
   - If `attemptsMade < maxAttempts`: Schedule retry (call retryJob.lua)
   - If `attemptsMade >= maxAttempts`: Move to failed (DLQ)
4. If moving to failed:
   - Update job hash: `HSET failedReason stacktrace finishedOn`
   - Add to failed set: `ZADD bull:{queue}:failed {timestamp} {jobId}`
   - Handle retention policy (similar to completed)
5. Release lock
6. Return: `"1"` (success) or `"retry"` (job scheduled for retry)

**Return Values**:
- **Failed (no retry)**: `1`
- **Scheduled for retry**: `"retry"`
- **Lock mismatch**: Error

**Example**:
```redis
EVALSHA <sha> 4 "bull:{myqueue}:2" "bull:{myqueue}:2:lock" "bull:{myqueue}:failed" "bull:{myqueue}:wait" "token" "Connection timeout" "[\"Error: ...\"]" "1698765010000" "3" "3" "0"
# Returns: 1 (max attempts reached, move to failed)
```

**Atomicity Guarantees**:
- Job either retries or moves to failed (not both)
- Failed count accurately reflects max attempts
- No lost jobs between active and failed states

---

### 4. retryJob.lua

**Purpose**: Atomically schedule job for retry with exponential backoff delay

**Signature**:
```lua
EVALSHA <sha> 2 <job-key> <delayed-key> <attemptsMade> <backoffType> <backoffDelay> <timestamp>
```

**KEYS**:
- `KEYS[1]`: `bull:{queue}:{jobId}` (HASH)
- `KEYS[2]`: `bull:{queue}:delayed` (ZSET)

**ARGV**:
- `ARGV[1]`: Attempts made (stringified integer)
- `ARGV[2]`: Backoff type (`"fixed"` or `"exponential"`)
- `ARGV[3]`: Base backoff delay in milliseconds (stringified integer)
- `ARGV[4]`: Current timestamp (Unix milliseconds)

**Algorithm**:
1. Calculate retry delay:
   - **Fixed**: `delay = backoffDelay`
   - **Exponential**: `delay = min(backoffDelay * 2^(attemptsMade-1), 3600000)`
     - Example: attempt 1: 1s, attempt 2: 2s, attempt 3: 4s, ..., max: 1 hour
2. Update job hash:
   - `HINCRBY bull:{queue}:{jobId} attemptsMade 1`
   - Append to stacktrace array
3. Calculate scheduled time:
   - `scheduledTime = timestamp + delay`
4. Add to delayed queue:
   - `ZADD bull:{queue}:delayed {scheduledTime} {jobId}`
5. Return: `{delay, scheduledTime}`

**Return Values**:
- **Success**: `[delay, scheduledTime]` (array of 2 integers)

**Example**:
```redis
EVALSHA <sha> 2 "bull:{myqueue}:2" "bull:{myqueue}:delayed" "1" "exponential" "1000" "1698765000000"
# Returns: [1000, 1698765001000] (1 second delay)

# Attempt 2:
# Returns: [2000, 1698765002000] (2 second delay)

# Attempt 10:
# Returns: [3600000, 1698768600000] (capped at 1 hour)
```

**Atomicity Guarantees**:
- attemptsMade increment and delayed queue addition are atomic
- No duplicate retry scheduling

---

### 5. moveStalledJobsToWait.lua

**Purpose**: Detect jobs with expired locks and requeue them for processing

**Signature**:
```lua
EVALSHA <sha> 1 <active-key> <timestamp> <lockDuration>
```

**KEYS**:
- `KEYS[1]`: `bull:{queue}:active` (LIST)

**ARGV**:
- `ARGV[1]`: Current timestamp (Unix milliseconds)
- `ARGV[2]`: Lock duration in milliseconds (e.g., `30000`)

**Algorithm**:
1. Get all active jobs:
   - `LRANGE bull:{queue}:active 0 -1`
2. For each job:
   - Check lock: `TTL bull:{queue}:{jobId}:lock`
   - If lock expired (TTL <= 0):
     - Remove from active list: `LREM bull:{queue}:active 1 {jobId}`
     - Add to wait queue: `RPUSH bull:{queue}:wait {jobId}`
     - Increment attemptsMade: `HINCRBY bull:{queue}:{jobId} attemptsMade 1`
     - Emit "stalled" event
3. Return: Array of stalled job IDs

**Return Values**:
- **Success**: `[jobId1, jobId2, ...]` (array of stalled job IDs, may be empty)

**Example**:
```redis
EVALSHA <sha> 1 "bull:{myqueue}:active" "1698765000000" "30000"
# Returns: ["3", "5"] (jobs 3 and 5 were stalled)
```

**Performance Optimization**:
- **Skip overlapping cycles**: If previous cycle still running, skip current cycle
- **Metric**: Track `bullmq_stalled_checker_skipped_total`
- **Target**: Complete within 100ms for 10,000 active jobs

**Atomicity Guarantees**:
- Each job requeue is atomic (lock check → requeue)
- No job loss (either stays active or moves to wait)

---

### 6. extendLock.lua

**Purpose**: Extend job lock TTL (heartbeat) to prevent stalled detection

**Signature**:
```lua
EVALSHA <sha> 1 <lock-key> <lockToken> <lockDuration>
```

**KEYS**:
- `KEYS[1]`: `bull:{queue}:{jobId}:lock` (STRING)

**ARGV**:
- `ARGV[1]`: Lock token (must match current lock value)
- `ARGV[2]`: Lock duration in seconds (e.g., `30`)

**Algorithm**:
1. Get current lock value:
   - `GET bull:{queue}:{jobId}:lock`
2. Verify lock ownership:
   - If doesn't match `ARGV[1]`, return error
3. Extend TTL:
   - `EXPIRE bull:{queue}:{jobId}:lock {lockDuration}`
4. Return: `"1"` (success)

**Return Values**:
- **Success**: `1`
- **Lock mismatch**: `redis.error_reply("Lock token mismatch")`
- **Lock not found**: `redis.error_reply("Lock not found")`

**Example**:
```redis
EVALSHA <sha> 1 "bull:{myqueue}:1:lock" "a1b2c3d4-..." "30"
# Returns: 1
```

**Heartbeat Timing**:
- **Lock TTL**: 30 seconds (configurable)
- **Heartbeat interval**: 15 seconds (50% of TTL, standard practice)
- **Failure policy**: Log error, continue processing (no circuit breaker)

**Atomicity Guarantees**:
- Lock token verification and TTL extension are atomic
- Prevents lock hijacking by another worker

---

### 7. updateProgress.lua

**Purpose**: Update job progress percentage

**Signature**:
```lua
EVALSHA <sha> 1 <job-key> <progress>
```

**KEYS**:
- `KEYS[1]`: `bull:{queue}:{jobId}` (HASH)

**ARGV**:
- `ARGV[1]`: Progress percentage (0-100, stringified number)

**Algorithm**:
1. Update job hash:
   - `HSET bull:{queue}:{jobId} progress {progress}`
2. Emit "progress" event to events stream
3. Return: `"1"` (success)

**Return Values**:
- **Success**: `1`
- **Invalid progress**: Error if progress < 0 or > 100

**Example**:
```redis
EVALSHA <sha> 1 "bull:{myqueue}:1" "50"
# Returns: 1
```

**Usage Pattern**:
```go
job.UpdateProgress(25)  // 25% complete
// ... processing ...
job.UpdateProgress(75)  // 75% complete
```

---

### 8. addLog.lua

**Purpose**: Append log entry to job logs list

**Signature**:
```lua
EVALSHA <sha> 1 <logs-key> <logMessage> <maxLogs>
```

**KEYS**:
- `KEYS[1]`: `bull:{queue}:{jobId}:logs` (LIST)

**ARGV**:
- `ARGV[1]`: Log message (string)
- `ARGV[2]`: Max logs to retain (e.g., `100`)

**Algorithm**:
1. Append log entry:
   - `RPUSH bull:{queue}:{jobId}:logs {logMessage}`
2. Trim list to max size:
   - `LTRIM bull:{queue}:{jobId}:logs -{maxLogs} -1`
   - Keeps last N logs (FIFO, oldest removed)
3. Return: `"1"` (success)

**Return Values**:
- **Success**: `1`

**Example**:
```redis
EVALSHA <sha> 1 "bull:{myqueue}:1:logs" "Started processing" "100"
# Returns: 1
```

**Usage Pattern**:
```go
job.Log("Connected to database")
job.Log("Sending email to user@example.com")
job.Log("Email sent successfully")
```

---

## Script Loading and Caching

### Go Implementation
```go
type ScriptLoader struct {
    client *redis.Client
    cache  map[string]string // scriptName -> SHA
    mu     sync.RWMutex
}

func (s *ScriptLoader) Load(ctx context.Context, name string, script string) (string, error) {
    s.mu.RLock()
    if sha, ok := s.cache[name]; ok {
        s.mu.RUnlock()
        return sha, nil
    }
    s.mu.RUnlock()

    // Load script and cache SHA
    sha, err := s.client.ScriptLoad(ctx, script).Result()
    if err != nil {
        return "", err
    }

    s.mu.Lock()
    s.cache[name] = sha
    s.mu.Unlock()

    return sha, nil
}

func (s *ScriptLoader) Exec(ctx context.Context, name string, keys []string, args []interface{}) (interface{}, error) {
    sha, err := s.Load(ctx, name, scripts[name])
    if err != nil {
        return nil, err
    }

    result, err := s.client.EvalSha(ctx, sha, keys, args...).Result()
    if err != nil {
        // Handle NOSCRIPT error (Redis restarted, scripts evicted)
        if strings.Contains(err.Error(), "NOSCRIPT") {
            // Reload script
            delete(s.cache, name)
            return s.Exec(ctx, name, keys, args)
        }
        return nil, err
    }

    return result, nil
}
```

---

## Error Handling

### Common Errors

1. **NOSCRIPT Error**:
   - **Cause**: Redis restarted, script evicted from cache
   - **Solution**: Reload script via `SCRIPT LOAD`, retry execution

2. **CROSSSLOT Error**:
   - **Cause**: Keys in different Redis Cluster slots
   - **Solution**: Ensure all keys use same hash tag `{queue-name}`

3. **Lock Token Mismatch**:
   - **Cause**: Lock expired, another worker acquired lock
   - **Solution**: Treat as stalled job, idempotent handler prevents duplicate work

4. **Script Timeout**:
   - **Cause**: Script execution exceeds Redis timeout
   - **Solution**: Optimize Lua script, increase Redis timeout, or break into smaller operations

---

## Testing Strategy

### Unit Tests (Lua)
- Test each script in isolation with mock Redis state
- Validate edge cases (empty queues, expired locks, invalid input)

### Integration Tests (Go)
- Execute scripts against real Redis instance
- Validate return values and side effects
- Test concurrent script execution (race conditions)

### Compatibility Tests (Node.js ↔ Go)
- Node.js producer → Go worker (consume jobs)
- Go producer → Node.js worker (consume jobs)
- Validate Redis state format matches exactly

---

## Performance Considerations

### Metrics

| Script | Target Latency | Notes |
|--------|---------------|-------|
| moveToActive.lua | < 10ms | Critical path (job pickup) |
| moveToCompleted.lua | < 10ms | Critical path (job completion) |
| extendLock.lua | < 10ms | High frequency (heartbeat) |
| moveStalledJobsToWait.lua | < 100ms | Bulk operation (10k jobs) |
| updateProgress.lua | < 10ms | Optional, best-effort |
| addLog.lua | < 10ms | Optional, best-effort |

### Optimization Tips

1. **Minimize Redis commands**: Each Redis command in Lua has overhead
2. **Use pipelining**: Group related operations
3. **Avoid loops**: Large loops (>1000 iterations) can block Redis
4. **Cache script SHAs**: EVALSHA faster than EVAL (no script transmission)
5. **Monitor SLOWLOG**: Identify slow scripts, optimize or break into smaller operations

---

## Version Compatibility

**Pinned Version**: BullMQ v5.62.0 (commit `6a31e0aeab1311d7d089811ede7e11a98b6dd408`)

**CI Validation**:
```bash
# Automated script verification on every build
./scripts/validate-lua-scripts.sh

# Steps:
1. Fetch official BullMQ scripts from GitHub (commit 6a31e0a)
2. Compute SHA256 of each script
3. Compare with pinned SHAs in Go constants
4. Fail build if mismatch detected
```

**Migration Path**:
- When BullMQ protocol changes, update commit SHA
- Re-extract scripts, update Go constants
- Run full compatibility test suite (Node.js ↔ Go)
- Update documentation with breaking changes

---

## References

- [BullMQ v5.62.0 Source](https://github.com/taskforcesh/bullmq/tree/6a31e0aeab1311d7d089811ede7e11a98b6dd408/src/scripts)
- [Redis Lua Scripting](https://redis.io/docs/manual/programmability/eval-intro/)
- [Redis EVALSHA](https://redis.io/commands/evalsha/)
- [Redis SCRIPT LOAD](https://redis.io/commands/script-load/)

---

**Last Updated**: 2025-10-30
**Maintained By**: BullMQ Go Client Library Team
