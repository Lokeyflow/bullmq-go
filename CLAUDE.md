# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## ⚠️ CRITICAL: BullMQ Protocol Compliance

**RULE: We MUST NOT deviate from the official BullMQ protocol definition.**

Before planning or implementing ANY changes:
1. **Check the BullMQ protocol specification** at [BullMQ Docs](https://docs.bullmq.io/)
2. **Verify against Node.js implementation** at [BullMQ GitHub](https://github.com/taskforcesh/bullmq)
3. **Validate field names, data structures, and Redis keys** match exactly
4. **Test cross-language compatibility** with Node.js BullMQ

### Protocol Version Compatibility

- **Target BullMQ Version**: v5.62.0 (released 2025-10-28)
- **Commit SHA**: `6a31e0aeab1311d7d089811ede7e11a98b6dd408`
- **Why pinned**: Exact commit pinning prevents protocol drift and ensures reproducible builds
- **Lua Scripts**: Extracted from official BullMQ repository at pinned version
- **CI Validation**: Automated checks compare scripts to upstream commit on every build

**Protocol-Critical Fields** (MUST match exactly):
- Job hash fields: `returnvalue` (lowercase, NOT `returnValue`)
- Event stream fields: `returnvalue`, `jobId`, `failedReason`, etc.
- Redis key format: `bull:queuename:*` or `bull:{queuename}:*` (cluster)
- Queue states: `wait`, `active`, `completed`, `failed`, `delayed`, `prioritized`

**When adding new features:**
- ✅ **Allowed**: Helper methods, convenience APIs, Go-specific patterns (e.g., `ProcessWithResults()`)
- ✅ **Allowed**: Application-level patterns built on top of protocol (e.g., results queue pattern)
- ❌ **Forbidden**: Changing Redis data structures, modifying Lua scripts, altering field names
- ❌ **Forbidden**: Adding new protocol features not in BullMQ Node.js
- ⚠️ **Verify**: Any feature that stores data in Redis or emits events must match protocol exactly

**Example - Results Queue Pattern**:
- ✅ Implementing `ProcessWithResults()` helper (calls standard `Queue.Add()`)
- ✅ Documented as application pattern, not protocol feature
- ❌ Creating special Redis keys for results
- ❌ Modifying protocol's `returnvalue` behavior

## Project Overview

**bullmq-go** is a Go client library for [BullMQ](https://github.com/taskforcesh/bullmq), providing protocol-compatible job queue functionality using Redis. This library enables Go applications to produce and consume jobs from BullMQ queues, with full interoperability with Node.js BullMQ workers and producers.

## Project Type

Standalone Go library (not an application). The library provides:
- **Worker** API for consuming jobs from BullMQ queues
- **Producer** API for adding jobs to BullMQ queues
- **Queue Manager** API for queue operations (pause, resume, clean, etc.)
- Full BullMQ protocol compatibility via Lua scripts

## Architecture

### Core Components

```
pkg/bullmq/
├── worker.go          - Job consumer with heartbeat and stalled detection
├── producer.go        - Job producer with priority, delay, and scheduling
├── queue.go           - Queue management operations
├── keys.go            - Redis key builder with hash tag support
├── job.go             - Job data structures (Job, JobOptions, BackoffConfig)
├── events.go          - Event emission to Redis streams
├── heartbeat.go       - Lock heartbeat for job ownership
├── stalled.go         - Stalled job detection and recovery
├── retry.go           - Retry logic with exponential backoff
├── errors.go          - Error categorization (transient vs permanent)
└── scripts/           - BullMQ Lua scripts for atomic operations
    ├── moveToActive.lua
    ├── moveToCompleted.lua
    ├── moveToFailed.lua
    ├── retryJob.lua
    ├── moveStalledJobsToWait.lua
    ├── extendLock.lua
    ├── updateProgress.lua
    └── addLog.lua
```

### Key Design Principles

1. **Protocol Compatibility**: Use official BullMQ Lua scripts (extracted from Node.js repo) for atomic Redis operations
2. **Redis Cluster Support**: Automatic hash tag detection - uses `{queue-name}` format only when connected to Redis Cluster
3. **Cross-Language Compatibility**: Auto-detection ensures jobs are visible between Node.js and Go implementations on both single-instance and cluster Redis
4. **Atomicity**: All state transitions use Lua scripts (not MULTI/EXEC) for proper atomicity
5. **Error Handling**: Categorize errors as transient (retry) or permanent (fail immediately)
6. **Graceful Degradation**: Worker survives Redis disconnects, graceful shutdown, crash recovery via stalled detection

### Redis Key Format Auto-Detection

The library automatically detects whether you're using single-instance Redis or Redis Cluster and adjusts key formats accordingly:

**Single-Instance Redis** (default):
```
bull:myqueue:wait        # No hash tags (compatible with Node.js BullMQ)
bull:myqueue:active
bull:myqueue:1           # Job ID
```

**Redis Cluster** (auto-detected):
```
bull:{myqueue}:wait      # Hash tags ensure same slot
bull:{myqueue}:active
bull:{myqueue}:1
```

**How it works**:
- KeyBuilder uses type assertion: `_, isCluster := client.(*redis.ClusterClient)`
- If cluster detected, all keys use `{queue-name}` hash tag syntax
- If single-instance, no hash tags are added (matches Node.js behavior)
- This ensures **cross-language compatibility** - Go workers can process jobs created by Node.js producers and vice versa

**Explicit override** (advanced):
```go
// Force hash tags ON or OFF regardless of client type
kb := bullmq.NewKeyBuilderWithHashTags("myqueue", true)  // Always use hash tags
kb := bullmq.NewKeyBuilderWithHashTags("myqueue", false) // Never use hash tags
```

### Job Lifecycle

```
Submitted → wait/prioritized → active (locked) → completed/failed/stalled
                                  ↓ (lock expired)
                                stalled → wait (retry)
```

- **wait queue** (LIST): FIFO for jobs without priority
- **prioritized queue** (ZSET): Priority-based processing (higher priority first)
- **delayed queue** (ZSET): Scheduled jobs (processed at specific time)
- **active list** (LIST): Currently processing jobs
- **completed/failed** (ZSET): Terminal states

## Development Commands

### Prerequisites

```bash
# Check Go version (requires 1.21+)
go version

# Check Redis version (requires 6.0+)
redis-cli --version
```

### Setup

```bash
# Install dependencies
go mod download

# Start Redis (for local testing)
# Option 1: Docker
docker run -d -p 6379:6379 redis:7-alpine

# Option 2: Docker Compose
docker-compose up -d redis

# Option 3: Rancher Desktop (Kubernetes)
kubectl run redis --image=redis:7-alpine --port=6379
kubectl port-forward pod/redis 6379:6379

# Option 4: Rancher Desktop (nerdctl)
nerdctl run -d -p 6379:6379 redis:7-alpine
```

### Redis Cluster Setup

**Redis Cluster** is a distributed implementation of Redis that automatically shards data across multiple nodes, providing high availability and horizontal scalability. For production deployments handling high throughput, Redis Cluster is recommended.

#### Why Redis Cluster?

- **Horizontal Scaling**: Distribute data across multiple nodes (up to 1000 nodes)
- **High Availability**: Automatic failover with replica promotion
- **Data Sharding**: Automatic partitioning using hash slots (16384 slots)

#### Requirements

- **Minimum 3 master nodes** (Redis Cluster requirement)
- **Hash tags** on all keys for multi-key operations (automatically validated by this library)

#### Local Cluster Setup (Docker Compose)

Create a `docker-compose.cluster.yml` file:

```yaml
version: '3.8'

services:
  redis-cluster:
    image: redis:7-alpine
    command: redis-cli --cluster create
      redis-node-1:6379 redis-node-2:6379 redis-node-3:6379
      --cluster-replicas 0 --cluster-yes
    depends_on:
      - redis-node-1
      - redis-node-2
      - redis-node-3

  redis-node-1:
    image: redis:7-alpine
    command: redis-server --port 6379 --cluster-enabled yes --cluster-config-file nodes.conf --cluster-node-timeout 5000
    ports:
      - "6379:6379"

  redis-node-2:
    image: redis:7-alpine
    command: redis-server --port 6379 --cluster-enabled yes --cluster-config-file nodes.conf --cluster-node-timeout 5000
    ports:
      - "6380:6379"

  redis-node-3:
    image: redis:7-alpine
    command: redis-server --port 6379 --cluster-enabled yes --cluster-config-file nodes.conf --cluster-node-timeout 5000
    ports:
      - "6381:6379"
```

Start the cluster:

```bash
docker-compose -f docker-compose.cluster.yml up -d
```

#### Connecting to Redis Cluster

```go
import "github.com/redis/go-redis/v9"

// Single-instance Redis (development)
client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

// Redis Cluster (production)
client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{
        "localhost:6379",
        "localhost:6380",
        "localhost:6381",
    },
})

// Use with BullMQ (works with both single-instance and cluster)
worker := bullmq.NewWorker("myqueue", client, bullmq.WorkerOptions{...})
```

#### Hash Tag Validation

This library **automatically validates** that all queue keys use proper hash tags on Worker startup:

```go
worker.Start(ctx)
// Output if using Redis Cluster:
// ✅ Redis Cluster detected: Queue 'myqueue' keys validated (slot 2331)
```

**How it works**:
- All queue keys use the format `bull:{queue-name}:...`
- The `{queue-name}` syntax is a Redis Cluster **hash tag**
- Redis only hashes the content between `{}` when calculating slots
- This ensures all keys for a queue hash to the **same slot**
- Multi-key Lua scripts require all keys to be in the same slot

**Example**:
```go
// All these keys hash to the same slot:
bull:{myqueue}:wait        → slot 2331
bull:{myqueue}:active      → slot 2331
bull:{myqueue}:prioritized → slot 2331
bull:{myqueue}:1           → slot 2331 (job hash)
bull:{myqueue}:1:lock      → slot 2331 (lock key)
```

Without hash tags, multi-key operations would fail with `CROSSSLOT` errors in cluster mode.

#### Verifying Cluster Health

```bash
# Connect to any cluster node
redis-cli -c -p 6379

# Check cluster status
CLUSTER INFO

# List cluster nodes
CLUSTER NODES

# Check key slot assignment
CLUSTER KEYSLOT "bull:{myqueue}:wait"
```

For more details on hash tag implementation, see the "Redis Keys" section below.

### Testing

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run tests with verbose output
go test -v ./...

# Run specific test
go test -run TestWorker_ProcessJob ./pkg/bullmq

# Run integration tests (requires Redis)
go test -tags=integration ./pkg/bullmq

# Run benchmarks
go test -bench=. ./pkg/bullmq
```

### Linting

```bash
# Run linter
golangci-lint run

# Run linter with auto-fix
golangci-lint run --fix

# Verify golangci-lint is installed
golangci-lint --version
```

### Building

```bash
# Build library (verify it compiles)
go build ./pkg/bullmq

# Run examples
go run examples/worker/main.go
go run examples/producer/main.go
```

## Implementation Guidelines

### Adding New Features

1. **Read specification first**: Check `001-bullmq-protocol-implementation/spec.md` for requirements
2. **Write tests first** (TDD): Create failing test before implementation
3. **Use Lua scripts**: For any multi-key atomic operations, use/extend Lua scripts
4. **Maintain compatibility**: Validate against Node.js BullMQ behavior
5. **Add metrics**: Use Prometheus metrics for observability

### Idempotency (CRITICAL)

**Job handlers MUST be idempotent** - they may be executed multiple times for the same job.

**Why Duplicate Execution Happens**:
1. Worker crash during processing → stalled checker requeues job
2. Lock expiration (heartbeat failure) → job requeued while still processing
3. Network partition (rare) → multiple workers pick up same job

**Library Guarantee**: At-least-once delivery (NOT exactly-once)

**Idempotent Patterns**:

```go
// Pattern 1: Idempotency key check
worker.Process(func(job *bullmq.Job) (interface{}, error) {
    jobID := job.ID

    // Check if already processed
    exists, _ := db.Exec("SELECT 1 FROM processed_jobs WHERE job_id = ?", jobID)
    if exists {
        return nil, nil // Already processed, skip
    }

    // Process job
    result := sendEmail(job.Data)

    // Mark as processed (atomic with business logic)
    db.Exec("INSERT INTO processed_jobs (job_id, result) VALUES (?, ?)", jobID, result)
    return result, nil
})

// Pattern 2: Database unique constraint
worker.Process(func(job *bullmq.Job) (interface{}, error) {
    orderID := job.Data["orderId"]

    // Insert with UNIQUE constraint on order_id
    // If duplicate, INSERT fails but operation is safe
    _, err := db.Exec(
        "INSERT INTO orders (order_id, status) VALUES (?, ?) ON CONFLICT DO NOTHING",
        orderID, "processed",
    )
    return map[string]interface{}{"orderId": orderID}, err
})

// Pattern 3: External system idempotency token
worker.Process(func(job *bullmq.Job) (interface{}, error) {
    // Stripe, PayPal, etc. support idempotency keys
    payment := stripe.CreateCharge(&stripe.ChargeParams{
        Amount:         job.Data["amount"],
        IdempotencyKey: job.ID, // Use job ID as idempotency token
    })
    return payment, payment.Error
})
```

**Non-Idempotent Example (AVOID)**:
```go
// BAD: Will send duplicate emails on retry
worker.Process(func(job *bullmq.Job) (interface{}, error) {
    sendEmail(job.Data["to"], job.Data["subject"])
    return nil, nil
})

// GOOD: Check if email already sent
worker.Process(func(job *bullmq.Job) (interface{}, error) {
    if !emailAlreadySent(job.ID) {
        sendEmail(job.Data["to"], job.Data["subject"])
        markEmailSent(job.ID)
    }
    return map[string]interface{}{"sent": true}, nil
})
```

### Error Handling

**Always categorize errors**:
- **Transient**: Network errors, timeouts, Redis failures, HTTP 5xx → retry
- **Permanent**: Validation errors, HTTP 4xx, auth errors → fail immediately

```go
// Example
if err := processJob(job); err != nil {
    category := CategorizeError(err)
    if category == ErrorCategoryTransient {
        // Retry with backoff
        return w.retry.Retry(ctx, job, err)
    }
    // Fail permanently
    return w.completer.Fail(ctx, job, err)
}
```

### Results Queue Pattern

**What is it**: An application-level pattern (NOT part of BullMQ protocol) for reliable result persistence.

**When to use**:
- Production systems requiring guaranteed result persistence
- Results that are expensive to recompute
- Microservice architectures with decoupled services
- Systems that need to survive service restarts

**Implementation**:
```go
// Explicit mode - ProcessWithResults()
worker.ProcessWithResults("results", func(job *Job) (interface{}, error) {
    result := processJob(job.Data)
    return result, nil // Auto-sent to "results" queue
}, ResultsQueueConfig{
    OnError: func(jobID string, err error) {
        log.Printf("Failed to send result: %v", err)
    },
})

// Implicit mode - WorkerOptions.ResultsQueue
worker := NewWorker("myqueue", rdb, WorkerOptions{
    ResultsQueue: &ResultsQueueConfig{
        QueueName: "results",
        Options: JobOptions{Attempts: 5}, // Retry result storage
    },
})
```

**Key Points**:
- This is a HELPER/CONVENIENCE feature, not protocol
- Uses standard BullMQ operations under the hood (Queue.Add)
- Result metadata includes: jobId, queueName, result, timestamp, processTime, attempt, workerId
- Failed jobs do NOT send to results queue (only successful completions)
- Original returnvalue still stored in job hash (BullMQ protocol)
- OnError callback is optional and won't stop job completion

**Testing**: See `tests/integration/results_queue_test.go` for comprehensive examples

### Lua Scripts

- **Never modify Lua scripts** without validating against Node.js BullMQ
- Scripts are extracted from [BullMQ repository](https://github.com/taskforcesh/bullmq/tree/master/src/scripts)
- **Version Compatibility**:
  - **Pinned Version**: v5.62.0 (released 2025-10-28)
  - **Commit SHA**: `6a31e0aeab1311d7d089811ede7e11a98b6dd408`
  - **Why**: Exact commit pinning prevents protocol drift and ensures reproducible builds
  - **CI Validation**: Automated check compares scripts to upstream commit on every build
- Scripts are loaded as Go constants in `pkg/bullmq/scripts/scripts.go`

### Redis Keys

All keys MUST use hash tags for cluster compatibility:

```go
// Correct
key := fmt.Sprintf("bull:{%s}:wait", queueName)

// Incorrect (breaks in Redis Cluster)
key := fmt.Sprintf("bull:%s:wait", queueName)
```

### Testing Strategy

1. **Unit tests**: Pure functions (error categorization, key building, backoff calculation)
2. **Integration tests**: Redis operations (use testcontainers-go for isolated Redis)
3. **Compatibility tests**: Validate against Node.js BullMQ (Node.js → Go, Go → Node.js)
4. **Load tests**: Performance validation (10+ concurrent workers, 100+ jobs)

## Common Tasks

### Adding a New Job Option

1. Add field to `JobOptions` struct in `pkg/bullmq/job.go`
2. Update Lua script arguments if needed
3. Add validation in job creation
4. Add test in `pkg/bullmq/job_test.go`
5. Update documentation

### Adding a New Queue Operation

1. Add method to `Queue` struct in `pkg/bullmq/queue.go`
2. Use appropriate Lua script or Redis commands
3. Ensure Redis Cluster compatibility (hash tags)
4. Add integration test
5. Document in README.md

### Debugging Redis State

```bash
# Connect to Redis CLI
redis-cli

# List all queue keys
KEYS bull:myqueue:*

# Inspect job hash
HGETALL bull:myqueue:1

# Check active jobs
LRANGE bull:myqueue:active 0 -1

# Check events stream
XRANGE bull:myqueue:events - + COUNT 10

# Check lock
GET bull:myqueue:1:lock
TTL bull:myqueue:1:lock
```

## Configuration

### Worker Configuration

```go
worker := bullmq.NewWorker(
    "myqueue",
    redisClient,
    bullmq.WorkerOptions{
        Concurrency:          10,              // Max concurrent jobs
        LockDuration:         30 * time.Second, // Lock TTL
        HeartbeatInterval:    15 * time.Second, // Lock heartbeat frequency
        StalledCheckInterval: 30 * time.Second, // Stalled job check frequency
        MaxAttempts:          3,                // Max retry attempts
        BackoffDelay:         time.Second,      // Base backoff delay
        WorkerID:             "",               // Auto-generated if empty: {hostname}-{pid}-{random}
        MaxReconnectAttempts: 0,                // 0 = unlimited (default), >0 = fail after N attempts
    },
)
```

### Redis Connection Loss Handling

**Retry Strategy**: Exponential backoff with jitter

```go
// Formula: min(initialDelay * 2^attempt * (0.8 + 0.4*rand()), maxDelay)
// Initial: 100ms, Max: 30s

Attempt 1:  100ms  ± 20% jitter = 80-120ms
Attempt 2:  200ms  ± 20% jitter = 160-240ms
Attempt 3:  400ms  ± 20% jitter = 320-480ms
Attempt 4:  800ms  ± 20% jitter = 640-960ms
Attempt 5:  1.6s   ± 20% jitter = 1.28-1.92s
Attempt 10: 30s    (capped) ± 20% = 24-36s
```

**Behavior**:
- Worker stops picking new jobs during disconnect
- Active jobs continue processing (use cached data)
- Heartbeat failures logged (jobs may stall if disconnect > 30s)
- Background reconnection with exponential backoff
- Resume after successful reconnect

**Configuration**:
- `MaxReconnectAttempts: 0` (default) = infinite retries, never give up
- `MaxReconnectAttempts: 10` = fail after 10 attempts (~60s total)

**WorkerID Generation**:
- **Auto-generated** (if not specified): `{hostname}-{pid}-{random6}`
- **Example**: `worker-node-1-12345-a1b2c3`
- **Purpose**: Observability and debugging (appears in logs, metrics, job.WorkerID field)
- **NOT used for**: Locking, uniqueness constraints, or business logic
- **Override**: Provide custom WorkerID via WorkerOptions.WorkerID if needed

### Important Timing Parameters

- **Lock TTL**: 30s (balance recovery speed vs network tolerance)
- **Heartbeat Interval**: 15s (50% of TTL, standard practice)
- **Stalled Check Interval**: 30s (detects failures within ~60s)

These values are based on research in `001-bullmq-protocol-implementation/research.md`.

## Documentation Structure

```
/
├── README.md                  - Public API documentation
├── CLAUDE.md                  - This file
├── CONTRIBUTING.md            - Contribution guidelines
├── examples/                  - Usage examples
│   ├── worker/                - Worker example
│   ├── producer/              - Producer example
│   └── queue/                 - Queue management example
└── 001-bullmq-protocol-implementation/  - Design documents
    ├── spec.md                - Feature specification
    ├── plan.md                - Implementation plan
    ├── data-model.md          - Data structures
    ├── tasks.md               - Task breakdown
    ├── research.md            - Design decisions
    └── contracts/             - Redis protocol contracts
        └── redis-protocol.md
```

## Dependencies

- **github.com/redis/go-redis/v9**: Redis client
- **github.com/google/uuid**: UUID generation for lock tokens
- **github.com/stretchr/testify**: Testing framework
- **github.com/testcontainers/testcontainers-go**: Integration testing with Redis

## Performance Targets

- **Job pickup latency**: < 10ms (moveToActive.lua)
- **Lock heartbeat**: < 10ms per extension
- **Stalled check**: < 100ms per cycle
- **Worker overhead**: < 5% latency increase vs simple queue

These targets are validated through load testing in `tests/load/`.

## Troubleshooting

### Worker not picking up jobs
- Check queue name matches producer
- Verify Redis connection
- Check if queue is paused: `HGET bull:{queue}:meta paused`
- Check if jobs are in correct queue: `LLEN bull:{queue}:wait` or `ZCARD bull:{queue}:prioritized`

### Jobs getting stalled
- Check heartbeat failures: `heartbeat_extend_failure_total` metric
- Verify lock TTL is appropriate for job duration
- Check network stability between worker and Redis
- **Note**: Heartbeat failures are logged but don't stop job processing
  - Worker continues processing even if heartbeat fails
  - Stalled checker requeues job if lock expires (30-60s recovery)
  - Idempotent job handlers prevent duplicate processing issues

### Compatibility issues with Node.js BullMQ
- Verify BullMQ version compatibility (currently targeting v5.x)
- Run compatibility tests: `npm run test:compatibility`
- Check Redis key format matches (use `redis-cli KEYS bull:*`)
- Validate event stream format: `XRANGE bull:{queue}:events - + COUNT 10`

## Further Reading

- [BullMQ Documentation](https://docs.bullmq.io/)
- [BullMQ GitHub Repository](https://github.com/taskforcesh/bullmq)
- [Redis Lua Scripts](https://redis.io/docs/manual/programmability/eval-intro/)
- [Redis Cluster Hash Tags](https://redis.io/docs/reference/cluster-spec/#hash-tags)
