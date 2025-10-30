# Redis Key Patterns - BullMQ Protocol

**Version**: BullMQ v5.62.0 (commit `6a31e0aeab1311d7d089811ede7e11a98b6dd408`)
**Date**: 2025-10-30
**Purpose**: Document all Redis key patterns used by BullMQ with hash tag support for Redis Cluster

---

## Overview

BullMQ uses a structured key naming convention to organize job queues and metadata in Redis. All keys for a given queue use Redis hash tags `{queue-name}` to ensure they map to the same cluster slot, enabling multi-key Lua script operations in Redis Cluster environments.

---

## Hash Tag Requirement

**CRITICAL**: All keys MUST use the format `bull:{queue-name}:*` where `{queue-name}` is the hash tag.

### Why Hash Tags?

Redis Cluster uses consistent hashing to distribute keys across nodes. When executing Lua scripts that operate on multiple keys (e.g., moving a job from wait to active), all keys must be on the same Redis node. Hash tags solve this by ensuring all keys with the same tag (the part inside `{}`) hash to the same slot.

**Example**:
```
bull:{myqueue}:wait       → Slot 7328
bull:{myqueue}:active     → Slot 7328 (same as above)
bull:{myqueue}:1          → Slot 7328 (same as above)
```

Without hash tags:
```
bull:myqueue:wait         → Slot 1234
bull:myqueue:active       → Slot 5678 (different!)
bull:myqueue:1            → Slot 9012 (different!)
```

This would cause `CROSSSLOT` errors when Lua scripts try to access multiple keys.

---

## Key Patterns

### 1. Queue Metadata

#### Queue Metadata Hash
```
bull:{queue-name}:meta
```

**Type**: HASH
**Purpose**: Store queue-level configuration and state
**Fields**:
- `paused` (string): "0" = running, "1" = paused
- `paused:id` (string): Identifier for pause operation
- `opts` (JSON string): Queue options (e.g., `{"maxLenEvents": 10000}`)

**Example**:
```redis
HGETALL bull:{notifications}:meta
1) "paused"
2) "0"
3) "opts"
4) "{\"maxLenEvents\":10000}"
```

#### Queue ID
```
bull:{queue-name}:id
```

**Type**: STRING (counter)
**Purpose**: Auto-incrementing counter for generating sequential job IDs
**Operations**: `INCR` to get next job ID

**Example**:
```redis
INCR bull:{notifications}:id
# Returns: 1, 2, 3, ...
```

---

### 2. Job Queues (Data Structures)

#### Wait Queue (FIFO)
```
bull:{queue-name}:wait
```

**Type**: LIST
**Purpose**: FIFO queue for jobs without priority
**Operations**: `RPUSH` (add), `LPOP` (retrieve)
**Contains**: Job IDs (e.g., "1", "2", "3")

**Example**:
```redis
LRANGE bull:{notifications}:wait 0 -1
1) "1"
2) "2"
3) "3"
```

#### Prioritized Queue
```
bull:{queue-name}:prioritized
```

**Type**: ZSET (sorted set)
**Purpose**: Priority queue for jobs with priority > 0
**Score**: Job priority (higher = processed first)
**Operations**: `ZADD` (add), `ZPOPMAX` (retrieve highest priority)
**Contains**: Job IDs with priority scores

**Example**:
```redis
ZRANGE bull:{notifications}:prioritized 0 -1 WITHSCORES
1) "5"
2) "10"    # Priority 10
3) "4"
4) "20"    # Priority 20
```

#### Delayed Queue
```
bull:{queue-name}:delayed
```

**Type**: ZSET (sorted set)
**Purpose**: Scheduled jobs waiting for specific timestamp
**Score**: Unix timestamp in milliseconds (when job should be processed)
**Operations**: `ZADD` (add), `ZRANGEBYSCORE` (retrieve jobs ready for processing)
**Contains**: Job IDs with timestamp scores

**Example**:
```redis
ZRANGE bull:{notifications}:delayed 0 -1 WITHSCORES
1) "6"
2) "1698765432000"  # Process at this timestamp
3) "7"
4) "1698769032000"
```

#### Active Queue
```
bull:{queue-name}:active
```

**Type**: LIST
**Purpose**: Jobs currently being processed by workers
**Operations**: `RPUSH` (add when picked up), `LREM` (remove when completed)
**Contains**: Job IDs

**Example**:
```redis
LRANGE bull:{notifications}:active 0 -1
1) "8"
2) "9"
```

---

### 3. Completed/Failed Jobs (Terminal States)

#### Completed Jobs
```
bull:{queue-name}:completed
```

**Type**: ZSET (sorted set)
**Purpose**: Successfully completed jobs (for history/auditing)
**Score**: Timestamp of completion (Unix milliseconds)
**Contains**: Job IDs

**Example**:
```redis
ZRANGE bull:{notifications}:completed 0 -1 WITHSCORES
1) "1"
2) "1698765000000"
3) "2"
4) "1698765005000"
```

**Retention**: Controlled by `removeOnComplete` option (e.g., keep last 1000 jobs)

#### Failed Jobs
```
bull:{queue-name}:failed
```

**Type**: ZSET (sorted set)
**Purpose**: Failed jobs (dead letter queue)
**Score**: Timestamp of failure (Unix milliseconds)
**Contains**: Job IDs

**Example**:
```redis
ZRANGE bull:{notifications}:failed 0 -1 WITHSCORES
1) "3"
2) "1698765010000"
```

**Retention**: Controlled by `removeOnFail` option

---

### 4. Job Data

#### Job Hash
```
bull:{queue-name}:{job-id}
```

**Type**: HASH
**Purpose**: Store all job data and metadata
**Fields**: See `job-schema.json` for complete structure

**Key Fields**:
- `name` (string): Job name/type
- `data` (JSON string): Job payload
- `opts` (JSON string): Job options
- `timestamp` (string): Creation timestamp (Unix ms)
- `attemptsMade` (string): Number of attempts
- `returnvalue` (JSON string): Result data (if completed)
- `failedReason` (string): Error message (if failed)
- `stacktrace` (array JSON): Error stack traces
- `processedOn` (string): Start timestamp
- `finishedOn` (string): Completion timestamp
- `progress` (string): Progress percentage (0-100)

**Example**:
```redis
HGETALL bull:{notifications}:1
 1) "name"
 2) "send-email"
 3) "data"
 4) "{\"to\":\"user@example.com\",\"subject\":\"Hello\"}"
 5) "opts"
 6) "{\"attempts\":3,\"backoff\":{\"type\":\"exponential\",\"delay\":1000}}"
 7) "timestamp"
 8) "1698765000000"
 9) "attemptsMade"
10) "0"
```

#### Job Logs
```
bull:{queue-name}:{job-id}:logs
```

**Type**: LIST
**Purpose**: Store log entries for a specific job
**Operations**: `RPUSH` (append), `LTRIM` (limit size)
**Contains**: Log message strings

**Example**:
```redis
LRANGE bull:{notifications}:1:logs 0 -1
1) "Started processing email"
2) "Connected to SMTP server"
3) "Email sent successfully"
```

**Retention**: Trimmed to prevent unbounded growth (default: last 100 logs)

---

### 5. Job Locks

#### Job Lock
```
bull:{queue-name}:{job-id}:lock
```

**Type**: STRING
**Purpose**: Distributed lock for job ownership
**Value**: UUID v4 lock token (e.g., `a1b2c3d4-e5f6-7890-abcd-ef1234567890`)
**TTL**: 30 seconds (configurable via `LockDuration`)
**Operations**: `SET NX EX` (acquire), `GET` (verify ownership), `DEL` (release)

**Example**:
```redis
GET bull:{notifications}:1:lock
# "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

TTL bull:{notifications}:1:lock
# 25 (seconds remaining)
```

**Heartbeat**: Workers extend lock TTL every 15 seconds via `extendLock.lua` script

---

### 6. Events Stream

#### Events Stream
```
bull:{queue-name}:events
```

**Type**: STREAM (Redis 5.0+)
**Purpose**: Pub/sub event stream for queue monitoring
**Operations**: `XADD` (emit event), `XREAD` (consume events), `XRANGE` (query history)
**Retention**: Approximately 10,000 events (MAXLEN ~10000)

**Event Entry Format**:
```redis
XRANGE bull:{notifications}:events - + COUNT 5
1) 1) "1698765000000-0"
   2) 1) "event"
      2) "waiting"
      3) "jobId"
      4) "1"
      5) "name"
      6) "send-email"

2) 1) "1698765001000-0"
   2) 1) "event"
      2) "active"
      3) "jobId"
      4) "1"
      5) "prev"
      6) "waiting"

3) 1) "1698765005000-0"
   2) 1) "event"
      2) "completed"
      3) "jobId"
      4) "1"
      5) "returnvalue"
      6) "{\"sent\":true}"
```

**Event Types**: See `event-schema.json` for complete event structure

---

## Key Lifecycle Examples

### Job Creation (Producer)
```
1. INCR bull:{myqueue}:id               → Generate job ID
2. HSET bull:{myqueue}:{id} ...         → Store job data
3. RPUSH bull:{myqueue}:wait {id}       → Add to wait queue (or ZADD for prioritized)
4. XADD bull:{myqueue}:events ...       → Emit "waiting" event
```

### Job Processing (Worker)
```
1. EVALSHA moveToActive.lua             → Move job from wait → active, acquire lock
2. GET bull:{myqueue}:{id}:lock         → Verify lock ownership
3. EVALSHA extendLock.lua (every 15s)   → Extend lock TTL (heartbeat)
4. EVALSHA moveToCompleted.lua          → Move job to completed, release lock
5. XADD bull:{myqueue}:events ...       → Emit "completed" event
```

### Job Stalled Recovery
```
1. LRANGE bull:{myqueue}:active 0 -1    → Get all active jobs
2. For each job:
   - TTL bull:{myqueue}:{id}:lock       → Check lock expiration
   - If expired:
     EVALSHA moveStalledJobsToWait.lua  → Requeue job
3. XADD bull:{myqueue}:events ...       → Emit "stalled" event
```

---

## Redis Cluster Slot Calculation

**Hash Slot Formula**: `CRC16(hash-tag) mod 16384`

**Example for `bull:{myqueue}:wait`**:
```
Hash tag: "myqueue"
CRC16("myqueue") = 7328
Slot = 7328 mod 16384 = 7328
```

All keys with `{myqueue}` hash to slot 7328, regardless of suffix.

---

## Testing Hash Tag Compliance

### Test Case 1: Verify All Keys Hash to Same Slot
```go
func TestHashTagCompliance(t *testing.T) {
    queueName := "testqueue"
    keys := []string{
        fmt.Sprintf("bull:{%s}:wait", queueName),
        fmt.Sprintf("bull:{%s}:active", queueName),
        fmt.Sprintf("bull:{%s}:completed", queueName),
        fmt.Sprintf("bull:{%s}:1", queueName),
        fmt.Sprintf("bull:{%s}:1:lock", queueName),
    }

    slots := make(map[uint16]bool)
    for _, key := range keys {
        slot := redis.ClusterSlot(key)
        slots[slot] = true
    }

    assert.Equal(t, 1, len(slots), "All keys must hash to same slot")
}
```

### Test Case 2: Validate Multi-Key Lua Script in Cluster
```bash
# In Redis Cluster CLI
redis-cli -c

# Should succeed (same slot)
EVAL "return redis.call('RPUSH', KEYS[1], '1')" 1 "bull:{myqueue}:wait"

# Should fail with CROSSSLOT error (different slots)
EVAL "return redis.call('RPUSH', KEYS[1], '1')" 1 "bull:myqueue:wait"
```

---

## Key Naming Violations (AVOID)

### Incorrect: No Hash Tags
```
bull:myqueue:wait          ❌ (different slots)
bull:myqueue:active        ❌ (different slots)
bull:myqueue:1             ❌ (different slots)
```

### Incorrect: Multiple Hash Tags
```
bull:{myqueue}:{otherqueue}:wait   ❌ (only first hash tag used)
```

### Correct: Consistent Hash Tags
```
bull:{myqueue}:wait        ✅
bull:{myqueue}:active      ✅
bull:{myqueue}:1           ✅
bull:{myqueue}:1:lock      ✅
```

---

## Key Expiration Policies

| Key Pattern | Expiration | Notes |
|-------------|-----------|-------|
| `bull:{queue}:meta` | None | Persistent queue config |
| `bull:{queue}:id` | None | Counter never expires |
| `bull:{queue}:wait` | None | Queue data structure |
| `bull:{queue}:active` | None | Queue data structure |
| `bull:{queue}:completed` | Size-based | ZREMRANGEBYRANK (keep last N) |
| `bull:{queue}:failed` | Size-based | ZREMRANGEBYRANK (keep last N) |
| `bull:{queue}:{id}` | Manual | Removed with job (removeOnComplete/Fail) |
| `bull:{queue}:{id}:lock` | 30s TTL | Auto-expires, extended by heartbeat |
| `bull:{queue}:{id}:logs` | Manual | Removed with job |
| `bull:{queue}:events` | Size-based | XTRIM MAXLEN ~10000 |

---

## Redis Cluster Compatibility Checklist

- [ ] All keys use format `bull:{queue-name}:*`
- [ ] Hash tag is always the queue name (not job ID or timestamp)
- [ ] Multi-key Lua scripts only access keys with same hash tag
- [ ] Integration tests validate CROSSSLOT errors don't occur
- [ ] CI runs tests against 3-node Redis Cluster

---

## References

- [BullMQ v5.62.0 Source](https://github.com/taskforcesh/bullmq/tree/6a31e0aeab1311d7d089811ede7e11a98b6dd408)
- [Redis Cluster Specification](https://redis.io/docs/reference/cluster-spec/)
- [Redis Hash Tags](https://redis.io/docs/reference/cluster-spec/#hash-tags)
- [Redis Cluster Slot Calculation](https://redis.io/docs/reference/cluster-spec/#keys-distribution-model)

---

**Last Updated**: 2025-10-30
**Maintained By**: BullMQ Go Client Library Team
