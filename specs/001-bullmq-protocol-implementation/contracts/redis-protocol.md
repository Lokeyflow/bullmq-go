# Redis Protocol Contracts

**Date**: 2025-10-28
**Feature**: 001-bullmq-protocol-implementation
**Phase**: Phase 1 (Contracts)

---

## Overview

This document defines the Redis protocol contracts between:

- **Frontend** (Node.js BullMQ Producer) → Redis
- **Worker** (Go BullMQ Consumer) → Redis

All operations use BullMQ-compatible Lua scripts for atomicity and protocol compliance.

---

## Contract 1: Job Submission

**Actor**: Frontend (Node.js BullMQ Producer)
**Direction**: Frontend → Redis
**Purpose**: Submit a new video generation job to the queue

### Redis Operations

```
1. INCR bull:{queue}:id
   → Returns: jobId (e.g., "1", "2", "3", ...)

2. HMSET bull:{queue}:{jobId}
   name "video-generation"
   data "{\"videoId\":\"...\",\"userId\":\"...\",\"script\":{...},\"userImageUrls\":[...]}"
   opts "{\"priority\":1,\"attempts\":3,\"backoff\":{\"type\":\"exponential\",\"delay\":1000},\"removeOnComplete\":false,\"removeOnFail\":false}"
   progress "0"
   delay "0"
   timestamp "1698765432000"
   attemptsMade "0"

3. IF priority > 0:
     ZADD bull:{queue}:prioritized {priority} {jobId}
   ELSE:
     RPUSH bull:{queue}:wait {jobId}

4. XADD bull:{queue}:events * \
     event "waiting" \
     jobId {jobId} \
     timestamp {now}
```

### Payload Schema

See [job-payload-schema.json](./job-payload-schema.json)

### Example

**Input** (Node.js):

```javascript
await queue.add('video-generation', {
  videoId: '550e8400-e29b-41d4-a716-446655440000',
  userId: 'user-123',
  script: {
    title: 'My Video',
    scenes: [
      {
        sceneNumber: 1,
        duration: 5,
        voiceover: 'Welcome',
        imagePrompt: 'Sunset'
      }
    ]
  },
  userImageUrls: ['https://example.com/img1.jpg']
}, {
  priority: 1,
  attempts: 3,
  backoff: { type: 'exponential', delay: 1000 }
});
```

**Redis State**:

```
GET bull:{video-generation}:id → "1"
HGETALL bull:{video-generation}:1 → {...}
ZRANGE bull:{video-generation}:prioritized 0 -1 → ["1"]
XLEN bull:{video-generation}:events → 1
```

---

## Contract 2: Job Pickup

**Actor**: Worker (Go BullMQ Consumer)
**Direction**: Worker → Redis (via `moveToActive.lua`)
**Purpose**: Atomically pick up the next job with lock acquisition

### Lua Script

**Script Name**: `moveToActive.lua` (from BullMQ repository)

**KEYS**:

```
KEYS[1] = bull:{queue}:wait
KEYS[2] = bull:{queue}:active
KEYS[3] = bull:{queue}:prioritized
KEYS[4] = bull:{queue}:delayed
KEYS[5] = bull:{queue}:paused
KEYS[6] = bull:{queue}:meta
KEYS[7] = bull:{queue}:events
... (additional keys for rate limiter, etc.)
```

**ARGV**:

```
ARGV[1] = timestamp (Unix ms)
ARGV[2] = lock token (UUID)
ARGV[3] = lock duration (milliseconds, e.g., 30000)
ARGV[4] = worker ID
... (additional args for rate limiter, etc.)
```

### Redis Operations (Atomic in Lua)

```lua
-- 1. Check pause state
local paused = redis.call("HGET", KEYS[6], "paused")
if paused == "1" then
    return nil  -- Queue paused, no job
end

-- 2. Check rate limit (if applicable)
-- ... (rate limiter logic)

-- 3. Get next job (prioritized first, then wait)
local jobId = redis.call("ZPOPMIN", KEYS[3], 1)
if not jobId or #jobId == 0 then
    jobId = redis.call("LPOP", KEYS[1])
end

if not jobId then
    return nil  -- No jobs available
end

-- 4. Acquire lock
local lockKey = "bull:{queue}:" .. jobId .. ":lock"
redis.call("SET", lockKey, ARGV[2], "PX", ARGV[3])

-- 5. Move to active
redis.call("RPUSH", KEYS[2], jobId)

-- 6. Update job metadata
local jobKey = "bull:{queue}:" .. jobId
redis.call("HMSET", jobKey,
    "processedOn", ARGV[1],
    "workerId", ARGV[4]
)
redis.call("HINCRBY", jobKey, "attemptsMade", 1)

-- 7. Emit active event
redis.call("XADD", KEYS[7], "*",
    "event", "active",
    "jobId", jobId,
    "timestamp", ARGV[1],
    "workerId", ARGV[4]
)

-- 8. Return job data
local jobData = redis.call("HGETALL", jobKey)
return {jobId, jobData}
```

### Go Usage

```go
keys := []string{
    keyBuilder.Wait(),
    keyBuilder.Active(),
    keyBuilder.Prioritized(),
    keyBuilder.Delayed(),
    keyBuilder.Paused(),
    keyBuilder.Meta(),
    keyBuilder.Events(),
}

args := []interface{}{
    time.Now().UnixMilli(),
    uuid.New().String(), // lock token
    30000, // lock TTL (30s)
    "worker-1",
}

result, err := redis.Eval(ctx, scripts.MoveToActive, keys, args...).Result()
// Parse result → BullMQJob
```

### Post-Conditions

- Job removed from :wait or :prioritized
- Job added to :active
- Lock created with token: `bull:{queue}:{jobId}:lock`
- Job hash updated: `processedOn`, `workerId`, `attemptsMade++`
- Event emitted: `{"event":"active","jobId":"...","timestamp":...}`

---

## Contract 3: Lock Heartbeat

**Actor**: Worker (Go BullMQ Consumer)
**Direction**: Worker → Redis (via `extendLock.lua`)
**Purpose**: Extend job lock to prevent stalled detection

### Lua Script

**Script Name**: `extendLock.lua` (custom, simple)

**KEYS**:

```
KEYS[1] = bull:{queue}:{jobId}:lock
```

**ARGV**:

```
ARGV[1] = lock token (must match current lock value)
ARGV[2] = lock TTL (milliseconds, e.g., 30000)
```

### Redis Operations (Atomic in Lua)

```lua
-- Only extend if we own the lock (token matches)
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
    return 0  -- Token mismatch or lock not found
end
```

### Go Usage

```go
lockKey := keyBuilder.JobLock(jobID)
args := []interface{}{
    lockToken, // from job pickup
    30000,     // lock TTL
}

result, err := redis.Eval(ctx, scripts.ExtendLock, []string{lockKey}, args...).Int()
if result == 0 {
    // Lock lost or token mismatch
}
```

### Frequency

- **Interval**: 15 seconds (50% of lock TTL)
- **Goroutine**: One per active job
- **Stop Condition**: Job completion or context cancellation

---

## Contract 4: Job Completion

**Actor**: Worker (Go BullMQ Consumer)
**Direction**: Worker → Redis (via `moveToCompleted.lua`)
**Purpose**: Mark job as successfully completed with result

### Lua Script

**Script Name**: `moveToCompleted.lua` (from BullMQ repository)

**KEYS**:

```
KEYS[1] = bull:{queue}:active
KEYS[2] = bull:{queue}:completed
KEYS[3] = bull:{queue}:{jobId} (job hash)
KEYS[4] = bull:{queue}:{jobId}:lock
KEYS[5] = bull:{queue}:events
```

**ARGV**:

```
ARGV[1] = jobId
ARGV[2] = returnvalue (JSON string)
ARGV[3] = timestamp (Unix ms)
ARGV[4] = removeOnComplete (true/false/number)
```

### Redis Operations (Atomic in Lua)

```lua
-- 1. Remove from active
redis.call("LREM", KEYS[1], 0, ARGV[1])

-- 2. Delete lock
redis.call("DEL", KEYS[4])

-- 3. Update job hash
redis.call("HMSET", KEYS[3],
    "returnvalue", ARGV[2],
    "finishedOn", ARGV[3]
)

-- 4. Add to completed (ZSET, score = timestamp)
redis.call("ZADD", KEYS[2], ARGV[3], ARGV[1])

-- 5. Emit completed event
redis.call("XADD", KEYS[5], "*",
    "event", "completed",
    "jobId", ARGV[1],
    "timestamp", ARGV[3],
    "returnvalue", ARGV[2]
)

-- 6. Handle removeOnComplete
if ARGV[4] == "true" then
    redis.call("DEL", KEYS[3])
elseif tonumber(ARGV[4]) then
    -- Keep last N jobs
    local count = redis.call("ZCARD", KEYS[2])
    if count > tonumber(ARGV[4]) then
        redis.call("ZREMRANGEBYRANK", KEYS[2], 0, -(tonumber(ARGV[4]) + 1))
    end
end

return 1
```

### Go Usage

```go
keys := []string{
    keyBuilder.Active(),
    keyBuilder.Completed(),
    keyBuilder.Job(jobID),
    keyBuilder.JobLock(jobID),
    keyBuilder.Events(),
}

result := map[string]interface{}{
    "videoUrl": "https://...",
    "duration": 15,
}
resultJSON, _ := json.Marshal(result)

args := []interface{}{
    jobID,
    string(resultJSON),
    time.Now().UnixMilli(),
    "false", // or "true" or 100 (keep last 100)
}

err := redis.Eval(ctx, scripts.MoveToCompleted, keys, args...).Err()
```

### Post-Conditions

- Job removed from :active
- Lock deleted
- Job hash updated: `returnvalue`, `finishedOn`
- Job added to :completed ZSET
- Event emitted: `{"event":"completed","jobId":"...","returnvalue":{...}}`
- Job hash optionally deleted (if removeOnComplete=true)

---

## Contract 5: Job Failure

**Actor**: Worker (Go BullMQ Consumer)
**Direction**: Worker → Redis (via `moveToFailed.lua` or `retryJob.lua`)
**Purpose**: Mark job as failed (permanently or retry)

### Case A: Permanent Failure (No Retry)

**Script Name**: `moveToFailed.lua` (from BullMQ repository)

**KEYS**:

```
KEYS[1] = bull:{queue}:active
KEYS[2] = bull:{queue}:failed
KEYS[3] = bull:{queue}:{jobId}
KEYS[4] = bull:{queue}:{jobId}:lock
KEYS[5] = bull:{queue}:events
```

**ARGV**:

```
ARGV[1] = jobId
ARGV[2] = failedReason (error message)
ARGV[3] = stacktrace (JSON array)
ARGV[4] = timestamp
ARGV[5] = removeOnFail (true/false/number)
```

**Operations** (similar to moveToCompleted, but to :failed):

```lua
redis.call("LREM", KEYS[1], 0, ARGV[1])
redis.call("DEL", KEYS[4])
redis.call("HMSET", KEYS[3],
    "failedReason", ARGV[2],
    "stacktrace", ARGV[3],
    "finishedOn", ARGV[4]
)
redis.call("ZADD", KEYS[2], ARGV[4], ARGV[1])
redis.call("XADD", KEYS[5], "*",
    "event", "failed",
    "jobId", ARGV[1],
    "failedReason", ARGV[2]
)
-- Handle removeOnFail...
```

### Case B: Retry

**Script Name**: `retryJob.lua` (from BullMQ repository)

**KEYS**:

```
KEYS[1] = bull:{queue}:active
KEYS[2] = bull:{queue}:wait (or :prioritized)
KEYS[3] = bull:{queue}:{jobId}
KEYS[4] = bull:{queue}:{jobId}:lock
KEYS[5] = bull:{queue}:events
```

**ARGV**:

```
ARGV[1] = jobId
ARGV[2] = delay (backoff milliseconds)
ARGV[3] = timestamp
```

**Operations**:

```lua
-- Check attempts
local attemptsMade = redis.call("HGET", KEYS[3], "attemptsMade")
local maxAttempts = redis.call("HGET", KEYS[3], "opts")  -- parse JSON for attempts

if tonumber(attemptsMade) >= maxAttempts then
    -- Move to failed (use moveToFailed.lua)
    return 0
end

-- Remove from active
redis.call("LREM", KEYS[1], 0, ARGV[1])
redis.call("DEL", KEYS[4])

-- Requeue with delay
if ARGV[2] > 0 then
    redis.call("ZADD", "bull:{queue}:delayed", ARGV[3] + ARGV[2], ARGV[1])
else
    redis.call("RPUSH", KEYS[2], ARGV[1])
end

-- Emit retry event
redis.call("XADD", KEYS[5], "*",
    "event", "retry",
    "jobId", ARGV[1],
    "delay", ARGV[2]
)

return 1
```

### Go Usage (Error Categorization)

```go
err := processJob(ctx, job)

if err != nil {
    if IsTransientError(err) && job.AttemptsMade < job.Opts.Attempts {
        // Retry with exponential backoff
        delay := calculateBackoff(job.Opts.Backoff, job.AttemptsMade)
        retryJob(ctx, job.ID, delay)
    } else {
        // Permanent failure or max attempts exceeded
        failJob(ctx, job.ID, err.Error(), removeOnFail)
    }
}
```

---

## Contract 6: Stalled Job Detection

**Actor**: Worker (Go BullMQ Consumer - Stalled Checker Goroutine)
**Direction**: Worker → Redis (via `moveStalledJobsToWait.lua`)
**Purpose**: Detect and requeue jobs whose locks have expired

### Lua Script

**Script Name**: `moveStalledJobsToWait.lua` (from BullMQ repository)

**KEYS**:

```
KEYS[1] = bull:{queue}:active
KEYS[2] = bull:{queue}:wait
KEYS[3] = bull:{queue}:prioritized
KEYS[4] = bull:{queue}:events
```

**ARGV**:

```
ARGV[1] = keyPrefix (e.g., "bull:{queue}:")
ARGV[2] = timestamp
```

### Redis Operations (Atomic in Lua)

```lua
local activeJobs = redis.call("LRANGE", KEYS[1], 0, -1)
local stalledCount = 0

for _, jobId in ipairs(activeJobs) do
    local lockKey = ARGV[1] .. jobId .. ":lock"
    local lockExists = redis.call("EXISTS", lockKey)

    if lockExists == 0 then
        -- Lock expired, job is stalled
        local jobKey = ARGV[1] .. jobId
        local jobData = redis.call("HGETALL", jobKey)

        if #jobData > 0 then
            -- Parse priority from job opts
            local priority = nil
            for i = 1, #jobData, 2 do
                if jobData[i] == "opts" then
                    -- Parse JSON to get priority (simplified)
                    -- priority = parseJSON(jobData[i+1]).priority
                end
            end

            -- Increment attempts
            redis.call("HINCRBY", jobKey, "attemptsMade", 1)

            -- Remove from active
            redis.call("LREM", KEYS[1], 0, jobId)

            -- Requeue
            if priority and priority > 0 then
                redis.call("ZADD", KEYS[3], priority, jobId)
            else
                redis.call("RPUSH", KEYS[2], jobId)
            end

            -- Emit stalled event
            redis.call("XADD", KEYS[4], "*",
                "event", "stalled",
                "jobId", jobId,
                "timestamp", ARGV[2]
            )

            stalledCount = stalledCount + 1
        end
    end
end

return stalledCount
```

### Go Usage

```go
// Run every 30 seconds in separate goroutine
ticker := time.NewTicker(30 * time.Second)
for range ticker.C {
    keys := []string{
        keyBuilder.Active(),
        keyBuilder.Wait(),
        keyBuilder.Prioritized(),
        keyBuilder.Events(),
    }
    args := []interface{}{
        keyBuilder.QueueKeyPrefix(),
        time.Now().UnixMilli(),
    }

    stalledCount, err := redis.Eval(ctx, scripts.MoveStalledJobsToWait, keys, args...).Int()
    if stalledCount > 0 {
        log.Info().Int("count", stalledCount).Msg("requeued stalled jobs")
    }
}
```

### Post-Conditions

- Stalled jobs (no lock) removed from :active
- Stalled jobs requeued to :wait or :prioritized
- `attemptsMade` incremented
- Events emitted: `{"event":"stalled","jobId":"..."}`

---

## Contract 7: Progress Update

**Actor**: Worker (Go BullMQ Consumer)
**Direction**: Worker → Redis (via `updateProgress.lua` or direct HMSET)
**Purpose**: Report job progress for UI visibility

### Redis Operations

**Option A: Direct (simple)**

```
HSET bull:{queue}:{jobId} progress {value}
XADD bull:{queue}:events * \
  event "progress" \
  jobId {jobId} \
  progress {value} \
  timestamp {now}
```

**Option B: Lua Script** (atomic)

```lua
-- KEYS[1] = job hash, KEYS[2] = events stream
redis.call("HSET", KEYS[1], "progress", ARGV[1])
redis.call("XADD", KEYS[2], "*",
    "event", "progress",
    "jobId", ARGV[2],
    "progress", ARGV[1],
    "timestamp", ARGV[3]
)
return 1
```

### Go Usage

```go
func UpdateProgress(ctx context.Context, jobID string, progress int, message string) error {
    // Validate: 0 ≤ progress ≤ 100
    if progress < 0 || progress > 100 {
        return errors.New("progress must be 0-100")
    }

    keys := []string{
        keyBuilder.Job(jobID),
        keyBuilder.Events(),
    }
    args := []interface{}{
        progress,
        jobID,
        time.Now().UnixMilli(),
    }

    return redis.Eval(ctx, scripts.UpdateProgress, keys, args...).Err()
}

// Usage in job handler
UpdateProgress(ctx, job.ID, 25, "Generating scene 1")
UpdateProgress(ctx, job.ID, 50, "Generating scene 2")
UpdateProgress(ctx, job.ID, 75, "Uploading video")
UpdateProgress(ctx, job.ID, 100, "Complete")
```

---

## Summary Table

| Contract | Lua Script | Triggers | Keys Modified | Events Emitted |
|----------|------------|----------|---------------|----------------|
| **Job Submission** | N/A (Node.js) | Frontend adds job | :id, :wait/:prioritized, job hash | waiting |
| **Job Pickup** | moveToActive.lua | Worker polls queue | :wait/:prioritized, :active, job hash, :lock | active |
| **Heartbeat** | extendLock.lua | Timer (15s interval) | :lock (TTL refresh) | None |
| **Completion** | moveToCompleted.lua | Job handler success | :active, :completed, job hash, :lock | completed |
| **Failure** | moveToFailed.lua | Job handler error (permanent) | :active, :failed, job hash, :lock | failed |
| **Retry** | retryJob.lua | Job handler error (transient) | :active, :wait/:delayed, job hash, :lock | retry |
| **Stalled Detection** | moveStalledJobsToWait.lua | Timer (30s interval) | :active, :wait/:prioritized, job hash | stalled |
| **Progress** | updateProgress.lua | Job handler milestone | job hash | progress |

---

**Status**: ✅ Redis Protocol Contracts Complete
**Next**: Generate JSON schemas for payloads and events
