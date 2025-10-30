# BullMQ-Go Integration Guide

This guide helps you integrate BullMQ-Go into your existing Go projects and deploy it to production.

## Table of Contents

- [Installation](#installation)
- [Architecture Patterns](#architecture-patterns)
- [Configuration](#configuration)
- [Error Handling](#error-handling)
- [Testing](#testing)
- [Monitoring](#monitoring)
- [Production Deployment](#production-deployment)
- [Common Pitfalls](#common-pitfalls)
- [Node.js Interoperability](#nodejs-interoperability)

---

## Installation

### Step 1: Add Dependency

```bash
go get github.com/Lokeyflow/bullmq-go/pkg/bullmq
```

### Step 2: Verify Redis Connection

```go
import (
    "context"
    "fmt"
    "github.com/redis/go-redis/v9"
)

func verifyRedis() error {
    client := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    defer client.Close()

    ctx := context.Background()
    if err := client.Ping(ctx).Err(); err != nil {
        return fmt.Errorf("redis connection failed: %w", err)
    }

    fmt.Println("✅ Redis connection successful")
    return nil
}
```

---

## Architecture Patterns

### Pattern 1: Monolith with Background Workers

**Use case**: Single application handles both HTTP requests and background jobs.

```
┌─────────────────────────────────┐
│      Your Go Application        │
│  ┌──────────┐    ┌───────────┐  │
│  │   HTTP   │    │  Workers  │  │
│  │ Handlers │───▶│(BullMQ-Go)│  │
│  └──────────┘    └───────────┘  │
│         │              │         │
└─────────┼──────────────┼─────────┘
          │              │
          ▼              ▼
     ┌─────────────────────┐
     │    Redis Queue      │
     └─────────────────────┘
```

**Implementation**:

```go
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"

    "github.com/Lokeyflow/bullmq-go/pkg/bullmq"
    "github.com/redis/go-redis/v9"
)

func main() {
    // Initialize Redis
    redisClient := redis.NewClient(&redis.Options{
        Addr: os.Getenv("REDIS_URL"),
    })
    defer redisClient.Close()

    // Initialize queue (for producers)
    queue := bullmq.NewQueue("tasks", redisClient)

    // Start worker in background
    worker := bullmq.NewWorker("tasks", redisClient, bullmq.WorkerOptions{
        Concurrency: 5,
    })
    worker.Process(processJob)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go worker.Start(ctx)

    // HTTP handler to submit jobs
    http.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
        job, err := queue.Add(r.Context(), "email", map[string]interface{}{
            "to": "user@example.com",
        }, bullmq.DefaultJobOptions)

        if err != nil {
            http.Error(w, err.Error(), 500)
            return
        }

        w.Write([]byte(job.ID))
    })

    // Graceful shutdown
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        <-sigCh
        log.Println("Shutting down...")
        cancel()
        worker.Stop()
        os.Exit(0)
    }()

    log.Fatal(http.ListenAndServe(":8080", nil))
}

func processJob(job *bullmq.Job) error {
    // Your job processing logic
    log.Printf("Processing job %s", job.ID)
    return nil
}
```

### Pattern 2: Separate Worker Service

**Use case**: Dedicated worker service for heavy background processing.

```
┌─────────────────┐        ┌─────────────────┐
│   API Service   │        │ Worker Service  │
│   (Producer)    │        │   (Consumer)    │
└────────┬────────┘        └────────┬────────┘
         │                          │
         ▼                          ▼
    ┌─────────────────────────────────┐
    │        Redis Queue              │
    └─────────────────────────────────┘
```

**API Service (Producer)**:

```go
// cmd/api/main.go
package main

import (
    "github.com/Lokeyflow/bullmq-go/pkg/bullmq"
    "github.com/redis/go-redis/v9"
)

func main() {
    redisClient := redis.NewClient(&redis.Options{
        Addr: os.Getenv("REDIS_URL"),
    })

    queue := bullmq.NewQueue("tasks", redisClient)

    // Only submit jobs, no workers
    http.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
        job, _ := queue.Add(r.Context(), "task", data, bullmq.DefaultJobOptions)
        json.NewEncoder(w).Encode(map[string]string{"jobId": job.ID})
    })

    http.ListenAndServe(":8080", nil)
}
```

**Worker Service (Consumer)**:

```go
// cmd/worker/main.go
package main

import (
    "context"
    "log"
    "github.com/Lokeyflow/bullmq-go/pkg/bullmq"
    "github.com/redis/go-redis/v9"
)

func main() {
    redisClient := redis.NewClient(&redis.Options{
        Addr: os.Getenv("REDIS_URL"),
    })

    worker := bullmq.NewWorker("tasks", redisClient, bullmq.WorkerOptions{
        Concurrency: 10,
    })

    worker.Process(func(job *bullmq.Job) error {
        log.Printf("Processing job %s", job.ID)
        // Heavy processing here
        return nil
    })

    log.Println("Worker started")
    worker.Start(context.Background())
}
```

### Pattern 3: Multiple Specialized Workers

**Use case**: Different workers for different job types.

```go
func main() {
    redisClient := redis.NewClient(&redis.Options{
        Addr: os.Getenv("REDIS_URL"),
    })

    ctx := context.Background()

    // Email worker
    emailWorker := bullmq.NewWorker("emails", redisClient, bullmq.WorkerOptions{
        Concurrency: 5,
    })
    emailWorker.Process(sendEmail)
    go emailWorker.Start(ctx)

    // Image processing worker
    imageWorker := bullmq.NewWorker("images", redisClient, bullmq.WorkerOptions{
        Concurrency: 2, // Heavy CPU work
    })
    imageWorker.Process(processImage)
    go imageWorker.Start(ctx)

    // PDF generation worker
    pdfWorker := bullmq.NewWorker("pdfs", redisClient, bullmq.WorkerOptions{
        Concurrency: 3,
    })
    pdfWorker.Process(generatePDF)
    go pdfWorker.Start(ctx)

    select {} // Keep running
}
```

---

## Configuration

### Development Configuration

```go
// config/dev.go
func NewDevConfig() *Config {
    return &Config{
        Redis: redis.Options{
            Addr: "localhost:6379",
        },
        Worker: bullmq.WorkerOptions{
            Concurrency:          2,
            LockDuration:         30 * time.Second,
            HeartbeatInterval:    15 * time.Second,
            StalledCheckInterval: 30 * time.Second,
            MaxAttempts:          3,
            BackoffDelay:         1 * time.Second,
            ShutdownTimeout:      10 * time.Second,
        },
        Queue: bullmq.JobOptions{
            Attempts: 3,
            Backoff: bullmq.BackoffConfig{
                Type:  "exponential",
                Delay: 1000,
            },
            RemoveOnComplete: true, // Clean up in dev
        },
    }
}
```

### Production Configuration

```go
// config/prod.go
func NewProdConfig() *Config {
    return &Config{
        Redis: redis.Options{
            Addr:         os.Getenv("REDIS_URL"),
            Password:     os.Getenv("REDIS_PASSWORD"),
            DB:           0,
            DialTimeout:  5 * time.Second,
            ReadTimeout:  3 * time.Second,
            WriteTimeout: 3 * time.Second,
            PoolSize:     20,
            MinIdleConns: 5,
        },
        Worker: bullmq.WorkerOptions{
            Concurrency:          10,
            LockDuration:         30 * time.Second,
            HeartbeatInterval:    15 * time.Second,
            StalledCheckInterval: 30 * time.Second,
            MaxAttempts:          5,
            BackoffDelay:         2 * time.Second,
            MaxBackoffDelay:      1 * time.Hour,
            ShutdownTimeout:      30 * time.Second,
        },
        Queue: bullmq.JobOptions{
            Attempts: 5,
            Backoff: bullmq.BackoffConfig{
                Type:  "exponential",
                Delay: 2000,
            },
            RemoveOnComplete: false, // Keep for debugging
        },
    }
}
```

### Environment Variables

```bash
# .env
REDIS_URL=localhost:6379
REDIS_PASSWORD=secret
WORKER_CONCURRENCY=10
JOB_MAX_ATTEMPTS=5
SHUTDOWN_TIMEOUT=30s
```

```go
import "github.com/kelseyhightower/envconfig"

type Config struct {
    RedisURL          string        `envconfig:"REDIS_URL" default:"localhost:6379"`
    RedisPassword     string        `envconfig:"REDIS_PASSWORD"`
    WorkerConcurrency int           `envconfig:"WORKER_CONCURRENCY" default:"5"`
    JobMaxAttempts    int           `envconfig:"JOB_MAX_ATTEMPTS" default:"3"`
    ShutdownTimeout   time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"30s"`
}

func LoadConfig() (*Config, error) {
    var cfg Config
    if err := envconfig.Process("", &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

---

## Error Handling

### Idempotent Job Handlers (CRITICAL)

**Job handlers MUST be idempotent** - they may execute multiple times for the same job due to:
- Worker crashes
- Lock expiration
- Network partitions

#### Pattern 1: Database Deduplication

```go
worker.Process(func(job *bullmq.Job) error {
    // Check if already processed
    exists, err := db.Query("SELECT 1 FROM processed_jobs WHERE job_id = ?", job.ID)
    if err != nil {
        return err // Retry
    }
    if exists {
        log.Printf("Job %s already processed, skipping", job.ID)
        return nil // Idempotent success
    }

    // Process job
    result, err := performWork(job.Data)
    if err != nil {
        return err // Will retry
    }

    // Mark as processed (atomic with work if using transactions)
    if err := db.Exec("INSERT INTO processed_jobs (job_id, result) VALUES (?, ?)", job.ID, result); err != nil {
        return err
    }

    return nil
})
```

#### Pattern 2: External Idempotency Keys

```go
import "github.com/stripe/stripe-go/v75"

worker.Process(func(job *bullmq.Job) error {
    // Use job ID as idempotency key
    params := &stripe.ChargeParams{
        Amount:   stripe.Int64(job.Data["amount"].(int64)),
        Currency: stripe.String("usd"),
    }
    params.SetIdempotencyKey(job.ID) // Stripe handles deduplication

    _, err := charge.New(params)
    return err
})
```

### Error Classification

```go
import "github.com/Lokeyflow/bullmq-go/pkg/bullmq"

worker.Process(func(job *bullmq.Job) error {
    result, err := callExternalAPI(job.Data)
    if err != nil {
        // Classify error
        if isValidationError(err) {
            // Permanent error - don't retry
            return &bullmq.PermanentError{
                Message: fmt.Sprintf("invalid data: %v", err),
            }
        }

        if isRateLimitError(err) {
            // Transient error - will retry with backoff
            return &bullmq.TransientError{
                Message: fmt.Sprintf("rate limited: %v", err),
                Cause:   err,
            }
        }

        // Default: transient (will retry)
        return err
    }

    return nil
})
```

### Retry Strategies

```go
// Custom retry for specific job types
queue.Add(ctx, "critical-task", data, bullmq.JobOptions{
    Attempts: 10, // Retry up to 10 times
    Backoff: bullmq.BackoffConfig{
        Type:  "exponential",
        Delay: 5000, // Start at 5 seconds
    },
})

// No retry for one-time jobs
queue.Add(ctx, "notification", data, bullmq.JobOptions{
    Attempts: 1, // Don't retry
})
```

---

## Testing

### Unit Testing Job Handlers

```go
// handlers/email.go
func SendEmailHandler(job *bullmq.Job) error {
    to := job.Data["to"].(string)
    subject := job.Data["subject"].(string)
    body := job.Data["body"].(string)

    return emailService.Send(to, subject, body)
}

// handlers/email_test.go
func TestSendEmailHandler(t *testing.T) {
    job := &bullmq.Job{
        ID: "test-123",
        Data: map[string]interface{}{
            "to":      "test@example.com",
            "subject": "Test",
            "body":    "Hello",
        },
    }

    err := SendEmailHandler(job)
    assert.NoError(t, err)
}
```

### Integration Testing with Redis

```go
// integration_test.go
func TestWorkerIntegration(t *testing.T) {
    // Use testcontainers for isolated Redis
    ctx := context.Background()
    redisContainer, _ := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: testcontainers.ContainerRequest{
            Image:        "redis:7-alpine",
            ExposedPorts: []string{"6379/tcp"},
        },
        Started: true,
    })
    defer redisContainer.Terminate(ctx)

    host, _ := redisContainer.Host(ctx)
    port, _ := redisContainer.MappedPort(ctx, "6379")

    client := redis.NewClient(&redis.Options{
        Addr: fmt.Sprintf("%s:%s", host, port.Port()),
    })

    // Test producer
    queue := bullmq.NewQueue("test", client)
    job, err := queue.Add(ctx, "test-job", map[string]interface{}{
        "data": "value",
    }, bullmq.DefaultJobOptions)
    assert.NoError(t, err)

    // Test worker
    processed := make(chan string, 1)
    worker := bullmq.NewWorker("test", client, bullmq.WorkerOptions{
        Concurrency: 1,
    })
    worker.Process(func(j *bullmq.Job) error {
        processed <- j.ID
        return nil
    })

    go worker.Start(ctx)

    select {
    case jobID := <-processed:
        assert.Equal(t, job.ID, jobID)
    case <-time.After(5 * time.Second):
        t.Fatal("Job not processed")
    }
}
```

---

## Monitoring

### Health Checks

```go
// /health endpoint
func healthHandler(queue *bullmq.Queue) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()

        // Check Redis connection
        if err := queue.redisClient.Ping(ctx).Err(); err != nil {
            http.Error(w, "Redis unhealthy", 503)
            return
        }

        // Check queue depth
        counts, err := queue.GetJobCounts(ctx)
        if err != nil {
            http.Error(w, "Cannot get job counts", 503)
            return
        }

        // Alert if too many failed jobs
        if counts.Failed > 100 {
            http.Error(w, "Too many failed jobs", 503)
            return
        }

        json.NewEncoder(w).Encode(map[string]interface{}{
            "status": "healthy",
            "counts": counts,
        })
    }
}
```

### Logging

```go
import "github.com/rs/zerolog/log"

worker.Process(func(job *bullmq.Job) error {
    logger := log.With().
        Str("job_id", job.ID).
        Str("job_name", job.Name).
        Str("worker_id", job.WorkerID).
        Logger()

    logger.Info().Msg("Job started")

    if err := processJob(job); err != nil {
        logger.Error().Err(err).Msg("Job failed")
        return err
    }

    logger.Info().Msg("Job completed")
    return nil
})
```

### Metrics (Future)

```go
// TODO: Add Prometheus metrics in Phase 14
import "github.com/prometheus/client_golang/prometheus"

var (
    jobsProcessed = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "bullmq_jobs_processed_total",
            Help: "Total jobs processed",
        },
        []string{"queue", "status"},
    )

    jobDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "bullmq_job_duration_seconds",
            Help: "Job processing duration",
        },
        []string{"queue"},
    )
)
```

---

## Production Deployment

### Docker Deployment

**Dockerfile**:

```dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o worker cmd/worker/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/

COPY --from=builder /app/worker .

CMD ["./worker"]
```

**docker-compose.yml**:

```yaml
version: '3.8'

services:
  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    volumes:
      - redis-data:/data
    command: redis-server --appendonly yes

  api:
    build:
      context: .
      dockerfile: Dockerfile.api
    ports:
      - "8080:8080"
    environment:
      - REDIS_URL=redis:6379
    depends_on:
      - redis

  worker:
    build:
      context: .
      dockerfile: Dockerfile.worker
    environment:
      - REDIS_URL=redis:6379
      - WORKER_CONCURRENCY=5
    depends_on:
      - redis
    deploy:
      replicas: 3 # Scale workers

volumes:
  redis-data:
```

### Kubernetes Deployment

**worker-deployment.yaml**:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bullmq-worker
spec:
  replicas: 3
  selector:
    matchLabels:
      app: bullmq-worker
  template:
    metadata:
      labels:
        app: bullmq-worker
    spec:
      containers:
      - name: worker
        image: your-registry/bullmq-worker:latest
        env:
        - name: REDIS_URL
          valueFrom:
            secretKeyRef:
              name: redis-secret
              key: url
        - name: WORKER_CONCURRENCY
          value: "10"
        resources:
          requests:
            memory: "128Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "1000m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
```

### Production Checklist

- [ ] **Redis Configuration**
  - [ ] Enable Redis persistence (AOF or RDB)
  - [ ] Configure Redis maxmemory and eviction policy
  - [ ] Set up Redis replication for high availability
  - [ ] Enable TLS for Redis connections

- [ ] **Worker Configuration**
  - [ ] Set appropriate concurrency based on workload
  - [ ] Configure retry limits and backoff
  - [ ] Set shutdown timeout for graceful restarts
  - [ ] Implement health check endpoints

- [ ] **Monitoring**
  - [ ] Set up logging aggregation (e.g., ELK, Loki)
  - [ ] Configure alerting for failed jobs
  - [ ] Monitor queue depth and processing lag
  - [ ] Track worker health and uptime

- [ ] **Error Handling**
  - [ ] Implement idempotent job handlers
  - [ ] Set up dead letter queue monitoring
  - [ ] Configure retry policies per job type
  - [ ] Log job failures with context

- [ ] **Security**
  - [ ] Use Redis authentication
  - [ ] Encrypt Redis connections (TLS)
  - [ ] Validate job payloads
  - [ ] Implement rate limiting

---

## Common Pitfalls

### 1. Non-Idempotent Job Handlers

❌ **Bad** - Sends duplicate emails on retry:

```go
worker.Process(func(job *bullmq.Job) error {
    sendEmail(job.Data["to"].(string))
    return nil
})
```

✅ **Good** - Checks if already sent:

```go
worker.Process(func(job *bullmq.Job) error {
    if emailAlreadySent(job.ID) {
        return nil
    }
    sendEmail(job.Data["to"].(string))
    markEmailSent(job.ID)
    return nil
})
```

### 2. Blocking Redis Connection Pool

❌ **Bad** - Reuses single Redis client for high-concurrency workers:

```go
client := redis.NewClient(&redis.Options{
    Addr:     "localhost:6379",
    PoolSize: 5, // Too small for 50 concurrent workers!
})

worker := bullmq.NewWorker("tasks", client, bullmq.WorkerOptions{
    Concurrency: 50,
})
```

✅ **Good** - Configures appropriate pool size:

```go
client := redis.NewClient(&redis.Options{
    Addr:         "localhost:6379",
    PoolSize:     100, // 2x concurrency
    MinIdleConns: 10,
})
```

### 3. Ignoring Context Cancellation

❌ **Bad** - Job runs to completion even after shutdown:

```go
worker.Process(func(job *bullmq.Job) error {
    for i := 0; i < 1000; i++ {
        processChunk(i) // Ignores context
        time.Sleep(1 * time.Second)
    }
    return nil
})
```

✅ **Good** - Respects context:

```go
worker.Process(func(job *bullmq.Job) error {
    ctx := job.Context // TODO: Add context to Job struct
    for i := 0; i < 1000; i++ {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            processChunk(i)
            time.Sleep(1 * time.Second)
        }
    }
    return nil
})
```

### 4. Forgetting to Pause Queue During Maintenance

```go
// Before deployment/maintenance
queue.Pause(ctx)

// Deploy new version...

// After deployment
queue.Resume(ctx)
```

---

## Node.js Interoperability

### Node.js Producer → Go Worker

**Node.js (Producer)**:

```javascript
const { Queue } = require('bullmq');

const queue = new Queue('tasks', {
  connection: { host: 'localhost', port: 6379 }
});

await queue.add('send-email', {
  to: 'user@example.com',
  subject: 'Hello from Node.js'
});
```

**Go (Worker)**:

```go
worker := bullmq.NewWorker("tasks", redisClient, bullmq.WorkerOptions{})

worker.Process(func(job *bullmq.Job) error {
    to := job.Data["to"].(string)
    subject := job.Data["subject"].(string)

    log.Printf("Processing Node.js job: %s", job.ID)
    return sendEmail(to, subject)
})

worker.Start(context.Background())
```

### Go Producer → Node.js Worker

**Go (Producer)**:

```go
queue := bullmq.NewQueue("tasks", redisClient)

job, _ := queue.Add(ctx, "send-email", map[string]interface{}{
    "to":      "user@example.com",
    "subject": "Hello from Go",
}, bullmq.DefaultJobOptions)
```

**Node.js (Worker)**:

```javascript
const { Worker } = require('bullmq');

const worker = new Worker('tasks', async (job) => {
  console.log(`Processing Go job: ${job.id}`);
  await sendEmail(job.data.to, job.data.subject);
}, {
  connection: { host: 'localhost', port: 6379 }
});
```

### Data Compatibility

✅ **Compatible Data Types**:
- Strings, numbers, booleans
- Objects/maps
- Arrays/slices

❌ **Avoid**:
- Go-specific types (channels, functions)
- Binary data without Base64 encoding
- Large nested structures (use references instead)

---

## Support

- **Documentation**: [README.md](README.md)
- **Quick Start**: [QUICKSTART.md](QUICKSTART.md)
- **Issues**: [GitHub Issues](https://github.com/Lokeyflow/bullmq-go/issues)
- **Discussions**: [GitHub Discussions](https://github.com/Lokeyflow/bullmq-go/discussions)

---

**Happy queueing! 🚀**
