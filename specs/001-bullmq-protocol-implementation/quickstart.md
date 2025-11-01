# BullMQ-Go Quickstart Guide

This guide will help you get started with BullMQ-Go, a production-ready Go client library for BullMQ that provides full protocol compatibility with Node.js BullMQ.

---

## Table of Contents

1. [Installation](#installation)
2. [Basic Worker Example](#basic-worker-example)
3. [Basic Producer Example](#basic-producer-example)
4. [Queue Management Example](#queue-management-example)
5. [Advanced Examples](#advanced-examples)
6. [Testing Example](#testing-example)
7. [Best Practices](#best-practices)

---

## Installation

Install the library using `go get`:

```bash
go get github.com/lokeyflow/bullmq-go/pkg/bullmq
```

**Requirements**:

- Go 1.21 or higher
- Redis 6.0 or higher
- (Optional) Node.js BullMQ v5.x for cross-language compatibility

**Start Redis** (if not already running):

```bash
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

---

## Basic Worker Example

A worker consumes jobs from a queue and processes them. This example shows how to create a worker with proper error handling and graceful shutdown.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/redis/go-redis/v9"
    "github.com/lokeyflow/bullmq-go/pkg/bullmq"
)

func main() {
    // 1. Connect to Redis
    redisClient := redis.NewClient(&redis.Options{
        Addr:     "localhost:6379",
        Password: "", // No password by default
        DB:       0,  // Default DB
    })

    // Test connection
    ctx := context.Background()
    if err := redisClient.Ping(ctx).Err(); err != nil {
        log.Fatalf("Failed to connect to Redis: %v", err)
    }
    defer redisClient.Close()

    // 2. Create worker with options
    worker := bullmq.NewWorker("email-queue", redisClient, bullmq.WorkerOptions{
        Concurrency:          10,              // Process up to 10 jobs concurrently
        LockDuration:         30 * time.Second, // Lock job for 30 seconds
        HeartbeatInterval:    15 * time.Second, // Extend lock every 15 seconds
        StalledCheckInterval: 30 * time.Second, // Check for stalled jobs every 30s
        MaxAttempts:          3,                // Retry failed jobs up to 3 times
        BackoffDelay:         time.Second,      // Base backoff delay: 1 second
        WorkerID:             "",               // Auto-generate worker ID
    })

    // 3. Define job processor (MUST be idempotent - may run multiple times!)
    worker.Process(func(job *bullmq.Job) error {
        fmt.Printf("[Worker] Processing job %s: %v\n", job.ID, job.Name)

        // Extract job data
        to := job.Data["to"].(string)
        subject := job.Data["subject"].(string)
        body := job.Data["body"].(string)

        // Update progress
        job.UpdateProgress(25)
        job.Log("Starting email send")

        // Simulate processing (replace with actual business logic)
        time.Sleep(2 * time.Second)

        // IMPORTANT: Implement idempotent logic here!
        // Check if email already sent using job.ID as idempotency key
        if emailAlreadySent(job.ID) {
            job.Log("Email already sent, skipping")
            return nil
        }

        // Send email
        if err := sendEmail(to, subject, body); err != nil {
            // Transient errors (network, timeout) will retry automatically
            job.Log(fmt.Sprintf("Failed to send email: %v", err))
            return err
        }

        // Mark email as sent (for idempotency)
        markEmailSent(job.ID)

        job.UpdateProgress(100)
        job.Log("Email sent successfully")

        return nil // Success
    })

    // 4. Handle errors
    worker.OnError(func(err error) {
        log.Printf("[Worker] Error: %v", err)
    })

    // 5. Graceful shutdown on SIGTERM/SIGINT
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Handle shutdown signals
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigChan
        log.Println("[Worker] Shutdown signal received, stopping worker...")
        cancel() // Cancel context to stop worker
    }()

    // 6. Start worker (blocks until context cancelled)
    log.Println("[Worker] Starting worker on queue 'email-queue'...")
    if err := worker.Start(ctx); err != nil {
        log.Fatalf("[Worker] Failed to start: %v", err)
    }

    log.Println("[Worker] Worker stopped gracefully")
}

// Idempotency helpers (implement with your database/cache)
func emailAlreadySent(jobID string) bool {
    // Check database/Redis for sent status
    // Example: SELECT 1 FROM sent_emails WHERE job_id = ?
    return false
}

func markEmailSent(jobID string) {
    // Mark in database/Redis that email was sent
    // Example: INSERT INTO sent_emails (job_id, sent_at) VALUES (?, NOW())
}

// Business logic
func sendEmail(to, subject, body string) error {
    // Replace with actual email sending logic
    fmt.Printf("Sending email to %s: %s\n", to, subject)
    return nil
}
```

**Key Points**:

- **Idempotency**: Job handlers MUST be idempotent (can run multiple times safely)
- **Concurrency**: Process multiple jobs in parallel with configurable concurrency
- **Graceful Shutdown**: Wait for active jobs to complete before stopping
- **Error Handling**: Transient errors (network, timeout) are retried automatically

---

## Basic Producer Example

A producer adds jobs to a queue. This example shows how to submit jobs with various options like priority, delay, and retry configuration.

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
    // 1. Connect to Redis
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })
    defer redisClient.Close()

    ctx := context.Background()
    if err := redisClient.Ping(ctx).Err(); err != nil {
        log.Fatalf("Failed to connect to Redis: %v", err)
    }

    // 2. Create queue
    queue := bullmq.NewQueue("email-queue", redisClient)

    // 3. Add basic job
    job1, err := queue.Add("send-email", map[string]interface{}{
        "to":      "user@example.com",
        "subject": "Welcome!",
        "body":    "Thank you for signing up.",
    }, bullmq.JobOptions{})

    if err != nil {
        log.Fatalf("Failed to add job: %v", err)
    }
    fmt.Printf("Job added: %s\n", job1.ID)

    // 4. Add job with priority (higher priority = processed first)
    job2, err := queue.Add("send-email", map[string]interface{}{
        "to":      "vip@example.com",
        "subject": "VIP Notification",
        "body":    "You have a new message.",
    }, bullmq.JobOptions{
        Priority: 10, // Higher priority
    })

    if err != nil {
        log.Fatalf("Failed to add priority job: %v", err)
    }
    fmt.Printf("Priority job added: %s (priority: %d)\n", job2.ID, job2.Opts.Priority)

    // 5. Add delayed job (scheduled for future processing)
    job3, err := queue.Add("send-email", map[string]interface{}{
        "to":      "user@example.com",
        "subject": "Reminder",
        "body":    "Don't forget to check your account!",
    }, bullmq.JobOptions{
        Delay: 5 * time.Minute, // Process 5 minutes from now
    })

    if err != nil {
        log.Fatalf("Failed to add delayed job: %v", err)
    }
    fmt.Printf("Delayed job added: %s (delay: 5m)\n", job3.ID)

    // 6. Add job with retry configuration
    job4, err := queue.Add("send-email", map[string]interface{}{
        "to":      "user@example.com",
        "subject": "Important Update",
        "body":    "Please read this important update.",
    }, bullmq.JobOptions{
        Attempts: 5, // Retry up to 5 times on failure
        Backoff: bullmq.BackoffConfig{
            Type:  "exponential", // Exponential backoff: 1s, 2s, 4s, 8s, 16s
            Delay: 1000,          // Base delay: 1000ms (1 second)
        },
    })

    if err != nil {
        log.Fatalf("Failed to add retry job: %v", err)
    }
    fmt.Printf("Retry job added: %s (attempts: %d, backoff: exponential)\n",
        job4.ID, job4.Opts.Attempts)

    // 7. Add job with cleanup options (auto-remove after completion/failure)
    job5, err := queue.Add("send-email", map[string]interface{}{
        "to":      "user@example.com",
        "subject": "Newsletter",
        "body":    "Check out this week's newsletter!",
    }, bullmq.JobOptions{
        RemoveOnComplete: true, // Remove job after successful completion
        RemoveOnFail:     false, // Keep failed jobs for debugging
    })

    if err != nil {
        log.Fatalf("Failed to add cleanup job: %v", err)
    }
    fmt.Printf("Cleanup job added: %s (removeOnComplete: true)\n", job5.ID)

    // 8. Add job with all options
    job6, err := queue.Add("send-email", map[string]interface{}{
        "to":      "admin@example.com",
        "subject": "Critical Alert",
        "body":    "System alert detected!",
    }, bullmq.JobOptions{
        Priority:         100,             // Highest priority
        Delay:            0,                // Process immediately
        Attempts:         3,                // Max 3 attempts
        Backoff: bullmq.BackoffConfig{
            Type:  "exponential",
            Delay: 2000, // 2s, 4s, 8s
        },
        RemoveOnComplete: 100,              // Keep last 100 completed jobs
        RemoveOnFail:     false,            // Keep all failed jobs
    })

    if err != nil {
        log.Fatalf("Failed to add job: %v", err)
    }
    fmt.Printf("Job added with all options: %s\n", job6.ID)

    // 9. Handle job submission errors
    _, err = queue.Add("send-email", map[string]interface{}{
        "to": "invalid-data",
    }, bullmq.JobOptions{
        Priority: -10, // Invalid: negative priority
    })

    if err != nil {
        fmt.Printf("Validation error (expected): %v\n", err)
    }

    fmt.Println("All jobs submitted successfully!")
}
```

**Key Points**:

- **Priority**: Higher priority jobs are processed first (default: 0)
- **Delay**: Schedule jobs for future processing
- **Retry**: Automatic retry with exponential backoff
- **Cleanup**: Auto-remove jobs after completion/failure to prevent Redis bloat
- **Validation**: Job options are validated before Redis write (fail fast)

---

## Queue Management Example

Queue management operations allow you to pause, resume, clean, and monitor queues.

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
    defer redisClient.Close()

    ctx := context.Background()

    // Create queue
    queue := bullmq.NewQueue("email-queue", redisClient)

    // 1. Get job counts
    fmt.Println("=== Job Counts ===")
    counts, err := queue.GetJobCounts(ctx)
    if err != nil {
        log.Fatalf("Failed to get job counts: %v", err)
    }
    fmt.Printf("Waiting: %d\n", counts.Waiting)
    fmt.Printf("Active: %d\n", counts.Active)
    fmt.Printf("Completed: %d\n", counts.Completed)
    fmt.Printf("Failed: %d\n", counts.Failed)
    fmt.Printf("Delayed: %d\n", counts.Delayed)
    fmt.Printf("Prioritized: %d\n", counts.Prioritized)

    // 2. Pause queue (stops job processing)
    fmt.Println("\n=== Pausing Queue ===")
    if err := queue.Pause(ctx); err != nil {
        log.Fatalf("Failed to pause queue: %v", err)
    }
    fmt.Println("Queue paused - workers will not pick up new jobs")

    // Check pause status
    isPaused, err := queue.IsPaused(ctx)
    if err != nil {
        log.Fatalf("Failed to check pause status: %v", err)
    }
    fmt.Printf("Queue paused: %v\n", isPaused)

    // 3. Resume queue
    fmt.Println("\n=== Resuming Queue ===")
    if err := queue.Resume(ctx); err != nil {
        log.Fatalf("Failed to resume queue: %v", err)
    }
    fmt.Println("Queue resumed - workers can now process jobs")

    // 4. Get job by ID
    fmt.Println("\n=== Get Job by ID ===")
    job, err := queue.GetJob(ctx, "job-123")
    if err != nil {
        fmt.Printf("Job not found: %v\n", err)
    } else {
        fmt.Printf("Job ID: %s\n", job.ID)
        fmt.Printf("Job Name: %s\n", job.Name)
        fmt.Printf("Job State: %s\n", job.State)
        fmt.Printf("Job Data: %v\n", job.Data)
        fmt.Printf("Progress: %d%%\n", job.Progress)
        fmt.Printf("Attempts: %d/%d\n", job.AttemptsMade, job.Opts.Attempts)
    }

    // 5. Remove job by ID
    fmt.Println("\n=== Remove Job ===")
    if err := queue.RemoveJob(ctx, "job-456"); err != nil {
        fmt.Printf("Failed to remove job: %v\n", err)
    } else {
        fmt.Println("Job removed successfully")
    }

    // 6. Clean old completed jobs (older than 24 hours)
    fmt.Println("\n=== Clean Completed Jobs ===")
    deletedCount, err := queue.Clean(ctx, 24*time.Hour, 1000, "completed")
    if err != nil {
        log.Fatalf("Failed to clean completed jobs: %v", err)
    }
    fmt.Printf("Cleaned %d completed jobs older than 24 hours\n", deletedCount)

    // 7. Clean old failed jobs (older than 7 days)
    fmt.Println("\n=== Clean Failed Jobs ===")
    deletedCount, err = queue.Clean(ctx, 7*24*time.Hour, 1000, "failed")
    if err != nil {
        log.Fatalf("Failed to clean failed jobs: %v", err)
    }
    fmt.Printf("Cleaned %d failed jobs older than 7 days\n", deletedCount)

    // 8. Drain queue (remove all jobs - USE WITH CAUTION!)
    fmt.Println("\n=== Drain Queue (DANGEROUS) ===")
    fmt.Println("Skipping drain operation (would remove ALL jobs)")
    // Uncomment to drain:
    // if err := queue.Drain(ctx); err != nil {
    //     log.Fatalf("Failed to drain queue: %v", err)
    // }
    // fmt.Println("All jobs removed from queue")

    // 9. Get jobs by state
    fmt.Println("\n=== Get Failed Jobs ===")
    failedJobs, err := queue.GetJobs(ctx, "failed", 0, 10)
    if err != nil {
        log.Fatalf("Failed to get failed jobs: %v", err)
    }
    fmt.Printf("Found %d failed jobs:\n", len(failedJobs))
    for i, job := range failedJobs {
        fmt.Printf("  %d. Job %s: %s (reason: %s)\n",
            i+1, job.ID, job.Name, job.FailedReason)
    }

    // 10. Retry failed job
    fmt.Println("\n=== Retry Failed Job ===")
    if len(failedJobs) > 0 {
        jobToRetry := failedJobs[0]
        if err := queue.RetryJob(ctx, jobToRetry.ID); err != nil {
            fmt.Printf("Failed to retry job: %v\n", err)
        } else {
            fmt.Printf("Job %s moved back to waiting queue\n", jobToRetry.ID)
        }
    }

    fmt.Println("\n=== Queue Management Complete ===")
}
```

**Key Points**:

- **Pause/Resume**: Control job processing without stopping workers
- **Clean**: Remove old completed/failed jobs to prevent Redis bloat
- **Job Retrieval**: Get job details and state
- **Retry**: Manually retry failed jobs
- **Monitoring**: Get real-time queue statistics

---

## Advanced Examples

### 5.1 Progress Reporting

Report job progress in real-time for long-running tasks:

```go
worker.Process(func(job *bullmq.Job) error {
    totalSteps := 5

    // Step 1: Initialize
    job.UpdateProgress(0)
    job.Log("Initializing task")
    time.Sleep(1 * time.Second)

    // Step 2: Download data
    job.UpdateProgress(20)
    job.Log("Downloading data from external API")
    time.Sleep(2 * time.Second)

    // Step 3: Process data
    job.UpdateProgress(40)
    job.Log("Processing downloaded data")
    time.Sleep(2 * time.Second)

    // Step 4: Transform data
    job.UpdateProgress(60)
    job.Log("Transforming data format")
    time.Sleep(1 * time.Second)

    // Step 5: Upload results
    job.UpdateProgress(80)
    job.Log("Uploading results to storage")
    time.Sleep(2 * time.Second)

    // Step 6: Finalize
    job.UpdateProgress(100)
    job.Log("Task completed successfully")

    return nil
})
```

**Monitor progress** (from another service):

```go
// Get job and check progress
job, err := queue.GetJob(ctx, jobID)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Job progress: %d%%\n", job.Progress)

// Read job logs
logs, err := queue.GetJobLogs(ctx, jobID)
if err != nil {
    log.Fatal(err)
}

for _, logEntry := range logs {
    fmt.Println(logEntry)
}
```

### 5.2 Log Collection

Collect structured logs during job processing:

```go
worker.Process(func(job *bullmq.Job) error {
    job.Log("Starting job processing")

    userID := job.Data["userId"].(string)
    job.Log(fmt.Sprintf("Processing for user: %s", userID))

    // Simulate processing with detailed logging
    for i := 1; i <= 5; i++ {
        job.Log(fmt.Sprintf("Processing batch %d/5", i))
        time.Sleep(1 * time.Second)

        if i == 3 {
            job.Log("Warning: Batch 3 took longer than expected")
        }
    }

    job.Log("Job completed successfully")
    return nil
})
```

**Log entry format** (stored in Redis):

```
[2025-10-30 10:15:32] Starting job processing
[2025-10-30 10:15:32] Processing for user: user-123
[2025-10-30 10:15:33] Processing batch 1/5
[2025-10-30 10:15:34] Processing batch 2/5
[2025-10-30 10:15:35] Processing batch 3/5
[2025-10-30 10:15:35] Warning: Batch 3 took longer than expected
[2025-10-30 10:15:36] Processing batch 4/5
[2025-10-30 10:15:37] Processing batch 5/5
[2025-10-30 10:15:37] Job completed successfully
```

### 5.3 Custom Retry Logic

Implement custom retry logic based on error type:

```go
worker.Process(func(job *bullmq.Job) error {
    err := processJob(job)
    if err != nil {
        // Categorize error
        category := bullmq.CategorizeError(err)

        switch category {
        case bullmq.ErrorCategoryTransient:
            // Transient errors: network timeout, Redis connection, HTTP 5xx
            job.Log(fmt.Sprintf("Transient error (will retry): %v", err))
            return err // Library will retry with exponential backoff

        case bullmq.ErrorCategoryPermanent:
            // Permanent errors: validation, HTTP 4xx, business logic
            job.Log(fmt.Sprintf("Permanent error (will not retry): %v", err))
            return &bullmq.PermanentError{
                Message: err.Error(),
            } // Job will fail immediately

        default:
            // Unknown error, default to retry
            job.Log(fmt.Sprintf("Unknown error (defaulting to retry): %v", err))
            return err
        }
    }

    return nil
})

func processJob(job *bullmq.Job) error {
    // Example: Call external API
    resp, err := http.Get("https://api.example.com/data")
    if err != nil {
        // Network error - transient
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode == 429 {
        // Rate limited - transient
        return &bullmq.TransientError{
            Message: "Rate limited by API",
        }
    }

    if resp.StatusCode >= 500 {
        // Server error - transient
        return &bullmq.TransientError{
            Message: fmt.Sprintf("API server error: %d", resp.StatusCode),
        }
    }

    if resp.StatusCode >= 400 && resp.StatusCode < 500 {
        // Client error - permanent
        return &bullmq.PermanentError{
            Message: fmt.Sprintf("API client error: %d", resp.StatusCode),
        }
    }

    // Success
    return nil
}
```

### 5.4 Idempotent Job Handlers

Implement idempotent handlers to safely handle duplicate execution:

```go
// Pattern 1: Database idempotency key check
worker.Process(func(job *bullmq.Job) error {
    jobID := job.ID

    // Check if job already processed (using database)
    var exists bool
    err := db.QueryRow(
        "SELECT EXISTS(SELECT 1 FROM processed_jobs WHERE job_id = ?)",
        jobID,
    ).Scan(&exists)

    if err != nil {
        return err
    }

    if exists {
        job.Log("Job already processed, skipping")
        return nil // Already processed, skip duplicate
    }

    // Process job
    result := sendEmail(job.Data)

    // Mark as processed (atomic with business logic in transaction)
    _, err = db.Exec(
        "INSERT INTO processed_jobs (job_id, result, processed_at) VALUES (?, ?, NOW())",
        jobID, result,
    )

    return err
})

// Pattern 2: Database unique constraint
worker.Process(func(job *bullmq.Job) error {
    orderID := job.Data["orderId"].(string)

    // INSERT with UNIQUE constraint on order_id
    // If duplicate, INSERT fails but operation is safe
    result, err := db.Exec(`
        INSERT INTO orders (order_id, status, created_at)
        VALUES (?, 'processed', NOW())
        ON CONFLICT (order_id) DO NOTHING
    `, orderID)

    if err != nil {
        return err
    }

    rowsAffected, _ := result.RowsAffected()
    if rowsAffected == 0 {
        job.Log("Order already processed (duplicate detected)")
    } else {
        job.Log("Order processed successfully")
    }

    return nil
})

// Pattern 3: External system idempotency token
worker.Process(func(job *bullmq.Job) error {
    amount := job.Data["amount"].(float64)

    // Stripe/PayPal support idempotency keys
    payment, err := stripe.CreateCharge(&stripe.ChargeParams{
        Amount:         int64(amount * 100), // Convert to cents
        Currency:       "usd",
        IdempotencyKey: job.ID, // Use job ID as idempotency token
    })

    if err != nil {
        return err
    }

    job.Log(fmt.Sprintf("Payment processed: %s", payment.ID))
    return nil
})

// Pattern 4: Redis SET NX (set if not exists)
worker.Process(func(job *bullmq.Job) error {
    jobID := job.ID
    lockKey := fmt.Sprintf("processed:%s", jobID)

    // Try to acquire lock (SET NX with expiration)
    acquired, err := redisClient.SetNX(ctx, lockKey, "1", 24*time.Hour).Result()
    if err != nil {
        return err
    }

    if !acquired {
        job.Log("Job already processed (Redis lock exists)")
        return nil // Already processed
    }

    // Process job
    err = sendEmail(job.Data)
    if err != nil {
        // Release lock on failure (so it can retry)
        redisClient.Del(ctx, lockKey)
        return err
    }

    job.Log("Job processed successfully")
    return nil
})
```

**Why Idempotency is Critical**:

- Worker crash during processing → job requeued by stalled checker
- Lock expiration during long processing → job picked up by another worker
- Network partition (rare) → multiple workers may pick up same job

---

## Testing Example

### 6.1 Integration Test with Testcontainers

Test BullMQ-Go with a real Redis instance using testcontainers:

```go
package bullmq_test

import (
    "context"
    "testing"
    "time"

    "github.com/redis/go-redis/v9"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/wait"
    "github.com/lokeyflow/bullmq-go/pkg/bullmq"
)

func TestWorkerIntegration(t *testing.T) {
    ctx := context.Background()

    // 1. Start Redis container
    redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: testcontainers.ContainerRequest{
            Image:        "redis:7-alpine",
            ExposedPorts: []string{"6379/tcp"},
            WaitingFor:   wait.ForLog("Ready to accept connections"),
        },
        Started: true,
    })
    require.NoError(t, err)
    defer redisContainer.Terminate(ctx)

    // Get Redis host and port
    host, err := redisContainer.Host(ctx)
    require.NoError(t, err)
    port, err := redisContainer.MappedPort(ctx, "6379")
    require.NoError(t, err)

    // 2. Connect to Redis
    redisClient := redis.NewClient(&redis.Options{
        Addr: fmt.Sprintf("%s:%s", host, port.Port()),
    })
    defer redisClient.Close()

    // 3. Create queue and add job
    queue := bullmq.NewQueue("test-queue", redisClient)
    job, err := queue.Add("test-job", map[string]interface{}{
        "message": "Hello, World!",
    }, bullmq.JobOptions{})
    require.NoError(t, err)

    // 4. Create worker and process job
    worker := bullmq.NewWorker("test-queue", redisClient, bullmq.WorkerOptions{
        Concurrency: 1,
    })

    jobProcessed := make(chan bool, 1)
    var processedJob *bullmq.Job

    worker.Process(func(job *bullmq.Job) error {
        processedJob = job
        jobProcessed <- true
        return nil
    })

    // Start worker
    go worker.Start(ctx)

    // 5. Wait for job to be processed
    select {
    case <-jobProcessed:
        // Success
        assert.Equal(t, job.ID, processedJob.ID)
        assert.Equal(t, "test-job", processedJob.Name)
        assert.Equal(t, "Hello, World!", processedJob.Data["message"])
    case <-time.After(5 * time.Second):
        t.Fatal("Timeout waiting for job to be processed")
    }

    // 6. Verify job completed
    completedJob, err := queue.GetJob(ctx, job.ID)
    require.NoError(t, err)
    assert.Equal(t, "completed", completedJob.State)
}

func TestJobRetry(t *testing.T) {
    ctx := context.Background()

    // Setup Redis (same as above)
    redisContainer, redisClient := setupRedis(t, ctx)
    defer redisContainer.Terminate(ctx)
    defer redisClient.Close()

    queue := bullmq.NewQueue("retry-queue", redisClient)

    // Add job with retry config
    job, err := queue.Add("retry-job", map[string]interface{}{
        "attempt": 0,
    }, bullmq.JobOptions{
        Attempts: 3,
        Backoff: bullmq.BackoffConfig{
            Type:  "exponential",
            Delay: 100, // 100ms base delay
        },
    })
    require.NoError(t, err)

    // Create worker that fails twice, then succeeds
    attemptCount := 0
    worker := bullmq.NewWorker("retry-queue", redisClient, bullmq.WorkerOptions{})

    worker.Process(func(job *bullmq.Job) error {
        attemptCount++

        if attemptCount < 3 {
            return fmt.Errorf("Simulated transient error (attempt %d)", attemptCount)
        }

        // Success on 3rd attempt
        return nil
    })

    go worker.Start(ctx)

    // Wait for job to complete (after retries)
    time.Sleep(2 * time.Second)

    // Verify job succeeded after retries
    completedJob, err := queue.GetJob(ctx, job.ID)
    require.NoError(t, err)
    assert.Equal(t, "completed", completedJob.State)
    assert.Equal(t, 3, attemptCount)
}
```

### 6.2 Mock Redis for Unit Tests

Unit test job processing logic without Redis:

```go
package myapp_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/lokeyflow/bullmq-go/pkg/bullmq"
)

// Test job processing logic in isolation
func TestProcessEmail(t *testing.T) {
    // Create mock job
    job := &bullmq.Job{
        ID:   "test-job-123",
        Name: "send-email",
        Data: map[string]interface{}{
            "to":      "test@example.com",
            "subject": "Test Email",
            "body":    "This is a test",
        },
    }

    // Test processing logic
    err := processEmail(job)
    assert.NoError(t, err)

    // Verify business logic (without Redis)
    assert.Equal(t, 100, job.Progress)
}

func TestProcessEmailValidation(t *testing.T) {
    // Test invalid data
    job := &bullmq.Job{
        ID:   "test-job-456",
        Name: "send-email",
        Data: map[string]interface{}{
            // Missing required fields
        },
    }

    err := processEmail(job)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "validation")
}

func processEmail(job *bullmq.Job) error {
    // Your job processing logic (isolated from BullMQ)
    to, ok := job.Data["to"].(string)
    if !ok || to == "" {
        return fmt.Errorf("validation error: 'to' field required")
    }

    subject, ok := job.Data["subject"].(string)
    if !ok || subject == "" {
        return fmt.Errorf("validation error: 'subject' field required")
    }

    // Process email
    job.UpdateProgress(100)
    return nil
}
```

---

## Best Practices

### 7.1 Job Handler Design

1. **ALWAYS make handlers idempotent** - jobs may run multiple times
2. **Keep handlers focused** - one job type = one responsibility
3. **Use timeouts** - prevent jobs from running forever
4. **Log important events** - use `job.Log()` for debugging
5. **Update progress** - for long-running jobs (>10s)

### 7.2 Error Handling

1. **Categorize errors correctly**:
   - **Transient**: Network, timeout, Redis connection → retry
   - **Permanent**: Validation, business logic → fail immediately
2. **Use exponential backoff** for transient errors
3. **Set appropriate max attempts** (default: 3)
4. **Monitor failed jobs** - implement dead letter queue alerts

### 7.3 Performance Optimization

1. **Set appropriate concurrency**:
   - CPU-bound jobs: `runtime.NumCPU()`
   - I/O-bound jobs: 10-50 (experiment with load testing)
2. **Use job cleanup** (`removeOnComplete`, `removeOnFail`)
3. **Monitor queue lengths** - alert on backlog growth
4. **Use priority queues** for time-sensitive jobs

### 7.4 Redis Configuration

1. **Use Redis 6.0+** for Lua script support
2. **Enable persistence** (AOF or RDB) for job durability
3. **Set appropriate `maxmemory-policy`**:
   - **Recommended**: `noeviction` (prevents job loss)
   - **Alternative**: `volatile-lru` (evicts expired keys only)
4. **Monitor Redis memory** - scale horizontally if needed

### 7.5 Monitoring & Alerting

1. **Track key metrics**:
   - Job processing rate
   - Job failure rate
   - Queue length (waiting, active)
   - Worker health (heartbeat failures)
2. **Set alerts**:
   - Queue length > threshold (backlog)
   - Failure rate > 5% (recurring errors)
   - Stalled jobs detected (worker crashes)
3. **Use Prometheus + Grafana** for visualization

### 7.6 Deployment

1. **Use multiple workers** for high availability
2. **Deploy workers separately from web servers**
3. **Use graceful shutdown** - wait for jobs to complete
4. **Scale horizontally** - add more workers as load increases
5. **Use health checks** - monitor worker process health

### 7.7 Security

1. **Validate job data** before processing
2. **Use Redis authentication** (`requirepass`)
3. **Limit job payload size** (max 10MB enforced)
4. **Sanitize logs** - avoid logging sensitive data
5. **Use TLS** for Redis connections in production

---

## Next Steps

1. **Explore Examples**: Check [examples/](../examples/) for more use cases
2. **Read API Docs**: [pkg.go.dev](https://pkg.go.dev/github.com/lokeyflow/bullmq-go)
3. **Join Community**: [GitHub Discussions](https://github.com/lokeyflow/bullmq-go/discussions)
4. **Report Issues**: [GitHub Issues](https://github.com/lokeyflow/bullmq-go/issues)

## Resources

- [BullMQ Documentation](https://docs.bullmq.io/)
- [Redis Documentation](https://redis.io/docs/)
- [Go Redis Client](https://github.com/redis/go-redis)
- [Testcontainers Go](https://golang.testcontainers.org/)

---

**Made with ❤️ for the Go community**
