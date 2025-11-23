# BullMQ-Go

[![Go Reference](https://pkg.go.dev/badge/github.com/lokeyflow/bullmq-go.svg)](https://pkg.go.dev/github.com/lokeyflow/bullmq-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/lokeyflow/bullmq-go)](https://goreportcard.com/report/github.com/lokeyflow/bullmq-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A Go client library for [BullMQ](https://github.com/taskforcesh/bullmq), providing full protocol compatibility with Node.js BullMQ. Build reliable, distributed job queues using Redis with Go workers and producers that seamlessly interoperate with Node.js BullMQ applications.

## Features

- **Full BullMQ Protocol Compatibility** - Works seamlessly with Node.js BullMQ producers and workers
- **Cross-Language Compatibility** - Auto-detects Redis mode and adjusts key formats for perfect interoperability
- **Worker API** - Consume jobs with configurable concurrency, retry logic, and stalled job recovery
- **Producer API** - Add jobs with priority, delay, and scheduling options
- **Results Queue Pattern** - Reliable result persistence with automatic forwarding to dedicated queues
- **Queue Management API** - Pause, resume, clean, and monitor queues
- **Atomic Operations** - Uses official BullMQ Lua scripts for race-free job state management
- **Redis Cluster Support** - Automatic hash tag detection and cluster-aware key formatting
- **Progress & Logs** - Real-time job progress tracking and log collection
- **Retry Logic** - Configurable exponential backoff with transient error detection
- **Observability** - Prometheus metrics and structured logging
- **Production Ready** - Graceful shutdown, heartbeat, stalled detection, and comprehensive testing

## Installation

```bash
go get github.com/lokeyflow/bullmq-go
```

## Quick Start

### Worker Example

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/redis/go-redis/v9"
    "github.com/lokeyflow/bullmq-go/pkg/bullmq"
)

func main() {
    // Connect to Redis
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })

    // Create worker
    worker := bullmq.NewWorker("myqueue", redisClient, bullmq.WorkerOptions{
        Concurrency: 10,
    })

    // Define job processor
    worker.Process(func(job *bullmq.Job) (interface{}, error) {
        fmt.Printf("Processing job %s: %v\n", job.ID, job.Data)

        // Your business logic here
        // Access job data: job.Data["key"]
        // Update progress: job.UpdateProgress(50)
        // Add logs: job.Log("Processing step 1")

        // Return result and error (nil, nil for success)
        result := map[string]interface{}{
            "processedAt": time.Now(),
            "status": "success",
        }
        return result, nil
    })

    // Start worker
    ctx := context.Background()
    if err := worker.Start(ctx); err != nil {
        log.Fatal(err)
    }
}
```

### Producer Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/redis/go-redis/v9"
    "github.com/lokeyflow/bullmq-go/pkg/bullmq"
)

func main() {
    // Connect to Redis
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })

    // Create queue
    queue := bullmq.NewQueue("myqueue", redisClient)

    // Add job
    job, err := queue.Add("send-email", map[string]interface{}{
        "to":      "user@example.com",
        "subject": "Welcome!",
        "body":    "Thank you for signing up.",
    }, bullmq.JobOptions{
        Priority: 5,
        Attempts: 3,
        Backoff: bullmq.BackoffConfig{
            Type:  "exponential",
            Delay: 1000, // milliseconds
        },
    })

    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Job added: %s\n", job.ID)
}
```

### Queue Management Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/redis/go-redis/v9"
    "github.com/lokeyflow/bullmq-go/pkg/bullmq"
)

func main() {
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })

    queue := bullmq.NewQueue("myqueue", redisClient)

    // Pause queue
    queue.Pause()

    // Resume queue
    queue.Resume()

    // Get job counts
    counts, _ := queue.GetJobCounts()
    fmt.Printf("Waiting: %d, Active: %d, Completed: %d, Failed: %d\n",
        counts.Waiting, counts.Active, counts.Completed, counts.Failed)

    // Clean old completed jobs (older than 24 hours)
    deleted, _ := queue.Clean(24*time.Hour, 1000, "completed")
    fmt.Printf("Cleaned %d completed jobs\n", deleted)

    // Get job by ID
    job, _ := queue.GetJob("job-123")
    fmt.Printf("Job status: %v\n", job)

    // Remove job
    queue.RemoveJob("job-456")
}
```

## Node.js Interoperability

BullMQ-Go is fully compatible with Node.js BullMQ. You can have Node.js producers and Go workers (or vice versa) working on the same queue.

### Node.js Producer → Go Worker

**Node.js (Producer)**:
```javascript
const { Queue } = require('bullmq');

const queue = new Queue('myqueue', {
  connection: { host: 'localhost', port: 6379 }
});

await queue.add('send-email', {
  to: 'user@example.com',
  subject: 'Hello',
  body: 'Message from Node.js'
});
```

**Go (Worker)**:
```go
worker := bullmq.NewWorker("myqueue", redisClient, bullmq.WorkerOptions{})

worker.Process(func(job *bullmq.Job) (interface{}, error) {
    to := job.Data["to"].(string)
    subject := job.Data["subject"].(string)
    body := job.Data["body"].(string)

    // Process the email
    err := sendEmail(to, subject, body)
    if err != nil {
        return nil, err
    }

    // Return result
    return map[string]interface{}{"sentTo": to}, nil
})

worker.Start(ctx)
```

### Automatic Redis Mode Detection

BullMQ-Go automatically detects whether you're using single-instance Redis or Redis Cluster and adjusts key formats accordingly. This ensures perfect compatibility with Node.js BullMQ in both environments:

**Single-Instance Redis** (default):
- Keys use format: `bull:myqueue:wait` (no hash tags)
- Matches Node.js BullMQ default behavior
- Go and Node.js workers/producers can interoperate seamlessly

**Redis Cluster**:
- Keys use format: `bull:{myqueue}:wait` (with hash tags)
- Hash tags ensure all queue keys hash to the same slot
- Enables multi-key Lua script execution in cluster mode

**How it works**:
```go
// Single-instance Redis - no hash tags
client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
queue := bullmq.NewQueue("myqueue", client)
// Keys: bull:myqueue:wait, bull:myqueue:active, ...

// Redis Cluster - automatic hash tags
cluster := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{"localhost:7001", "localhost:7002", "localhost:7003"},
})
queue := bullmq.NewQueue("myqueue", cluster)
// Keys: bull:{myqueue}:wait, bull:{myqueue}:active, ...
```

**Why this matters**:
- v0.1.0 always used hash tags, breaking compatibility with Node.js on single-instance Redis
- v0.1.1+ auto-detects and matches Node.js behavior in both modes
- Jobs created by Node.js are now visible to Go workers and vice versa

## Advanced Features

### Progress Tracking

```go
worker.Process(func(job *bullmq.Job) (interface{}, error) {
    job.UpdateProgress(0)
    job.Log("Starting processing")

    // Step 1
    doStep1()
    job.UpdateProgress(33)
    job.Log("Step 1 complete")

    // Step 2
    doStep2()
    job.UpdateProgress(66)
    job.Log("Step 2 complete")

    // Step 3
    result := doStep3()
    job.UpdateProgress(100)
    job.Log("Processing complete")

    return result, nil
})
```

### Delayed Jobs

```go
// Job will be processed 5 minutes from now
job, err := queue.Add("delayed-task", data, bullmq.JobOptions{
    Delay: 5 * time.Minute,
})
```

### Job Priority

```go
// Higher priority jobs are processed first
job, err := queue.Add("high-priority", data, bullmq.JobOptions{
    Priority: 10, // Higher number = higher priority
})

job, err := queue.Add("low-priority", data, bullmq.JobOptions{
    Priority: 1,
})
```

### Custom Retry Logic

```go
job, err := queue.Add("task", data, bullmq.JobOptions{
    Attempts: 5, // Retry up to 5 times
    Backoff: bullmq.BackoffConfig{
        Type:  "exponential", // or "fixed"
        Delay: 2000,          // Base delay: 2 seconds
    },
    // Retry delays: 2s, 4s, 8s, 16s, 32s
})
```

### Job Cleanup

```go
// Remove job after completion
job, err := queue.Add("task", data, bullmq.JobOptions{
    RemoveOnComplete: true,
})

// Keep last 100 completed jobs
job, err := queue.Add("task", data, bullmq.JobOptions{
    RemoveOnComplete: 100,
})

// Remove job after failure
job, err := queue.Add("task", data, bullmq.JobOptions{
    RemoveOnFail: true,
})
```

### Results Queue Pattern

For production systems that require reliable result persistence, BullMQ-Go provides the **results queue pattern** - a recommended practice where job results are automatically forwarded to a dedicated queue for downstream processing.

**Benefits**:
- Results persist in Redis until successfully processed
- Survives service restarts and temporary failures
- Automatic retries for failed result storage
- Decouples job processing from result persistence
- Perfect for microservices architecture

**Example - Explicit Mode (ProcessWithResults)**:
```go
// Video processing worker
videoWorker := bullmq.NewWorker("video-queue", redisClient, bullmq.WorkerOptions{
    Concurrency: 5,
})

videoWorker.ProcessWithResults("results", func(job *bullmq.Job) (interface{}, error) {
    videoURL := job.Data["videoURL"].(string)

    // Process video
    processedURL := processVideo(videoURL)

    // Result automatically sent to "results" queue with metadata
    return map[string]interface{}{
        "outputURL": processedURL,
        "duration":  123.45,
        "format":    "mp4",
    }, nil
}, bullmq.ResultsQueueConfig{
    OnError: func(jobID string, err error) {
        log.Printf("Failed to send result for job %s: %v", jobID, err)
    },
})

// Results worker - processes results from ALL queues
resultsWorker := bullmq.NewWorker("results", redisClient, bullmq.WorkerOptions{})

resultsWorker.Process(func(job *bullmq.Job) (interface{}, error) {
    jobID := job.Data["jobId"].(string)
    queueName := job.Data["queueName"].(string)
    result := job.Data["result"]
    processTime := job.Data["processTime"]

    // Store in database
    db.SaveResult(jobID, result)

    // Send webhooks
    webhooks.NotifyCompletion(result)

    return nil, nil
})
```

**Example - Implicit Mode (WorkerOptions)**:
```go
// Email worker with automatic result forwarding
emailWorker := bullmq.NewWorker("email-queue", redisClient, bullmq.WorkerOptions{
    Concurrency: 10,
    ResultsQueue: &bullmq.ResultsQueueConfig{
        QueueName: "results",
        Options: bullmq.JobOptions{
            Attempts: 5, // Retry result storage up to 5 times
        },
    },
})

emailWorker.Process(func(job *bullmq.Job) (interface{}, error) {
    // Result automatically forwarded to "results" queue
    return map[string]interface{}{
        "sent":      true,
        "messageId": "msg-123",
    }, nil
})
```

**Result Metadata** - Each result includes rich metadata:
```json
{
  "jobId": "12345",
  "queueName": "video-queue",
  "result": {
    "outputURL": "https://cdn.example.com/processed/12345.mp4",
    "duration": 123.45
  },
  "timestamp": 1699564800,
  "processTime": 2000,
  "attempt": 1,
  "workerId": "worker-1-12345-abc123"
}
```

See [examples/results-queue](./examples/results-queue/) for a complete working example.

### Error Handling

The library automatically categorizes errors as transient (retry) or permanent (fail immediately):

```go
worker.Process(func(job *bullmq.Job) (interface{}, error) {
    // Transient errors (will retry):
    // - Network timeouts
    // - Redis connection errors
    // - HTTP 5xx errors
    if err := callExternalAPI(); err != nil {
        return nil, err // Will retry with exponential backoff
    }

    // Permanent errors (will not retry):
    // - Validation errors
    // - HTTP 4xx errors
    // - Business logic violations
    if !isValid(job.Data) {
        return nil, &bullmq.ValidationError{Message: "Invalid data"}
    }

    return map[string]interface{}{"status": "success"}, nil
})
```

### Graceful Shutdown

```go
// Wait for active jobs to complete before shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

worker.Start(ctx)

// On SIGTERM/SIGINT:
worker.Stop() // Waits for active jobs, timeout after 30s
```

## Configuration

### Worker Options

```go
worker := bullmq.NewWorker("myqueue", redisClient, bullmq.WorkerOptions{
    // Max number of concurrent jobs
    Concurrency: 10,

    // Lock duration (how long a worker can hold a job)
    LockDuration: 30 * time.Second,

    // How often tolokeyflowhe lock (heartbeat)
    HeartbeatInterval: 15 * time.Second,

    // How often to check for stalled jobs
    StalledCheckInterval: 30 * time.Second,

    // Max retry attempts (default: 3)
    MaxAttempts: 3,

    // Base backoff delay (default: 1s)
    BackoffDelay: time.Second,
})
```

### Queue Options

```go
queue := bullmq.NewQueue("myqueue", redisClient, bullmq.QueueOptions{
    // Default job options applied to all jobs
    DefaultJobOptions: bullmq.JobOptions{
        Attempts: 3,
        Backoff: bullmq.BackoffConfig{
            Type:  "exponential",
            Delay: 1000,
        },
    },
})
```

## Observability

### Prometheus Metrics

```go
import "github.com/Lokeyflow/bullmq-go/pkg/bullmq/metrics"

// Register metrics with Prometheus
metrics.RegisterMetrics()

// Metrics exported:
// - bullmq_jobs_processed_total{queue, status}
// - bullmq_job_duration_seconds{queue}
// - bullmq_queue_length{queue, state}
// - bullmq_stalled_jobs_total{queue}
// - bullmq_heartbeat_success_total{queue}
// - bullmq_heartbeat_failure_total{queue}
```

### Structured Logging

```go
import "github.com/rs/zerolog/log"

worker := bullmq.NewWorker("myqueue", redisClient, bullmq.WorkerOptions{
    Logger: log.Logger, // Compatible with zerolog, zap, logrus
})
```

## Architecture

### Job Lifecycle

```
Submitted → wait/prioritized → active (locked) → completed/failed
                                  ↓ (lock expired)
                                stalled → wait (retry)
```

### Redis Keys

All keys use hash tags for Redis Cluster compatibility:

- `bull:{queue}:wait` - Jobs waiting for processing (LIST)
- `bull:{queue}:prioritized` - Jobs with priority (ZSET)
- `bull:{queue}:delayed` - Scheduled jobs (ZSET)
- `bull:{queue}:active` - Currently processing jobs (LIST)
- `bull:{queue}:completed` - Completed jobs (ZSET)
- `bull:{queue}:failed` - Failed jobs (ZSET)
- `bull:{queue}:{jobId}` - Job data (HASH)
- `bull:{queue}:{jobId}:lock` - Job lock (STRING)
- `bull:{queue}:events` - Job events (STREAM)

### Atomic Operations

The library uses official BullMQ Lua scripts for atomic state transitions:

- `moveToActive.lua` - Pick up job with lock acquisition
- `moveToCompleted.lua` - Complete job and store result
- `moveToFailed.lua` - Fail job and store error
- `retryJob.lua` - Retry job with exponential backoff
- `moveStalledJobsToWait.lua` - Detect and requeue stalled jobs
- `extendLock.lua` - Extend job lock (heartbeat)
- `updateProgress.lua` - Update job progress
- `addLog.lua` - Append job log

## Testing

### Run Tests

```bash
# Unit tests
go test ./pkg/bullmq

# Integration tests (requires Redis)
go test -tags=integration ./pkg/bullmq

# With coverage
go test -cover ./pkg/bullmq

# Benchmarks
go test -bench=. ./pkg/bullmq
```

### Compatibility Tests

```bash
# Test Node.js → Go interoperability
cd tests/compatibility
npm install
npm run test:node-to-go

# Test Go → Node.js interoperabilitylokeyflow
npm run test:go-to-node

# Shadow test (both workers running concurrently)
npm run test:shadow
```

## Requirements

- **Go**: 1.21 or higher
- **Redis**: 6.0 or higher (for Lua script support)
- **BullMQ** (Node.js): v5.x for cross-language compatibility

## Performance
lokeyflow
Based on load testing with 10 concurrent workers processing 1000+ jobs:

- **Job pickup latency**: < 10ms (p95)
- **Lock heartbeat**: < 10ms per extension
- **Stalled check**: < 100ms per cycle
- **Throughput**: 1000+ jobs/second per worker

## Examples

See the [examples/](./examples/) directory for complete working examples:

- [Worker](./examples/worker/) - Basic worker setup
- [Producer](./examples/producer/) - Job submission
- [Queue Management](./examples/queue/) - Queue operations
- [Results Queue](./examples/results-queue/) - Reliable result persistence pattern
- [Progress Tracking](./examples/progress/) - Real-time progress updates
- [Node.js Interop](./examples/nodejs-interop/) - Cross-language compatibility

## Documentation

- [API Documentation](https://pkg.go.dev/github.com/Lokeyflow/bullmq-go)
- [Specification](./001-bullmq-protocol-implementation/spec.md) - Complete feature specification
- [Data Model](./001-bullmq-protocol-implementation/data-model.md) - Job structure and Redis storage
- [Research](./001-bullmq-protocol-implementation/research.md) - Design decisions and protocol analysis
- [Redis Protocol](./001-bullmq-protocol-implementation/contracts/redis-protocol.md) - BullMQ protocol details

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

### Development Setup

```bash
# Clone repository
git clone https://github.com/Lokeyflow/bullmq-go.git
cd bullmq-go

# Install dependencies
go mod download

# Start Redis (for testing)
# Option 1: Docker
docker run -d -p 6379:6379 redis:7-alpine

# Option 2: Rancher Desktop (Kubernetes)
kubectl run redis --image=redis:7-alpine --port=6379
kubectl port-forward pod/redis 6379:6379

# Option 3: Rancher Desktop (nerdctl)
nerdctl run -d -p 6379:6379 redis:7-alpine
lokeyflow
# Run testslokeyflow
go test ./...

# Run linter
golangci-lint run
```

## Roadmap

- [x] Worker API with concurrency support
- [x] Producer API with job options
- [x] Queue management operations
- [x] Progress tracking and logging
- [x] Retry logic with exponential backoff
- [x] Stalled job detection and recovery
- [x] Node.js BullMQ compatibility
- [x] Redis Cluster support
- [ ] Repeatable jobs (cron-like scheduling)
- [ ] Job flows/dependencies
- [ ] Job groups with rate limiting
- [ ] Built-in metrics dashboard

## License

MIT License - see [LICENSE](./LICENSE) for details.

## Acknowledgments

- [BullMQ](https://github.com/taskforcesh/bullmq) - The original Node.js implementation
- [go-redis](https://github.com/redis/go-redis) - Redis client for Go

## Support

- **Issues**: [GitHub Issues](https://github.com/Lokeyflow/bullmq-go/issues)
- **Discussions**: [GitHub Discussions](https://github.com/Lokeyflow/bullmq-go/discussions)
- **BullMQ Documentation**: https://docs.bullmq.io/

## Status

**Current Version**: v0.2.0 (beta)

This library is under active development. The API is stabilizing but may change before v1.0.0 release. Use in production with caution and thorough testing.

---

**Made with ❤️ for the Go community**
