# Results Queue Pattern Example

This example demonstrates the **results queue pattern** - a recommended best practice for reliable result persistence in production systems.

## What is the Results Queue Pattern?

Instead of relying solely on `returnvalue` (which could be lost if event handlers fail), you send job results to a **dedicated results queue** for guaranteed downstream processing.

**Key Benefits:**
- ✅ Results persist in Redis until successfully processed
- ✅ Survives service restarts and temporary failures
- ✅ Automatic retries for failed result storage
- ✅ Decouples job processing from result persistence
- ✅ Perfect for microservices architecture

## How It Works

```
Job Queue → Worker → Results Queue → Results Worker → Database/Storage
            (process)  (forward)      (persist)
```

1. **Worker** processes job and returns result
2. **Result** automatically sent to results queue
3. **Results worker** reliably stores/forwards result
4. **Original result** also available in `job.returnvalue`

## Running This Example

### Prerequisites

```bash
# Start Redis
docker run -d -p 6379:6379 redis:7-alpine

# Or use existing Redis instance
redis-server
```

### Run the Example

```bash
cd examples/results-queue
go run main.go
```

### What You'll See

```
✅ Connected to Redis

📹 Starting video processing worker (explicit mode)...
📧 Starting email worker (implicit mode)...
💾 Starting results storage worker...

📝 Adding test jobs...
  ✅ Added video job: 1
  ✅ Added video job: 2
  ✅ Added video job: 3
  ✅ Added email job: 1
  ...

🚀 Starting all workers...
  🎬 Processing video: https://example.com/video1.mp4 (job 1)
  📨 Sending email to user1@example.com: Test Email #1
  ✅ Video processed: https://cdn.example.com/processed/1.mp4
  ✅ Email sent: msg-1
  💾 Storing result: job=1 queue=video-queue duration=2000ms
  💾 Storing result: job=1 queue=email-queue duration=500ms
```

## Two Usage Modes

### Mode 1: Explicit (ProcessWithResults)

```go
worker.ProcessWithResults("results", func(job *bullmq.Job) (interface{}, error) {
    result := processVideo(job.Data)
    return result, nil // Auto-sent to "results" queue
}, bullmq.ResultsQueueConfig{
    OnError: func(jobID string, err error) {
        log.Printf("Failed to send result: %v", err)
    },
})
```

**Use when:** You want explicit control and visibility

### Mode 2: Implicit (WorkerOptions)

```go
worker := bullmq.NewWorker("email-queue", rdb, bullmq.WorkerOptions{
    Concurrency: 10,
    ResultsQueue: &bullmq.ResultsQueueConfig{
        QueueName: "results",
    },
})

worker.Process(func(job *bullmq.Job) (interface{}, error) {
    return sendEmail(job.Data), nil // Auto-forwarded
})
```

**Use when:** You want implicit behavior for all jobs

## Results Worker Implementation

The results worker processes results from **all queues**:

```go
resultsWorker.Process(func(job *bullmq.Job) (interface{}, error) {
    jobID := job.Data["jobId"].(string)
    queueName := job.Data["queueName"].(string)
    result := job.Data["result"]
    processTime := job.Data["processTime"]

    // Store in database
    db.SaveResult(jobID, result)

    // Send webhooks
    webhooks.NotifyCompletion(result)

    // Update analytics
    analytics.Track(queueName, processTime)

    return nil, nil
})
```

## Result Metadata

Each result includes rich metadata:

```json
{
  "jobId": "12345",
  "queueName": "video-queue",
  "result": {
    "outputURL": "https://cdn.example.com/processed/12345.mp4",
    "duration": 123.45,
    "format": "mp4"
  },
  "timestamp": 1699564800,
  "processTime": 2000,
  "attempt": 1,
  "workerId": "worker-1-12345-abc123"
}
```

## When to Use This Pattern

✅ **Use results queue when:**
- Results must be reliably persisted (database writes)
- Results are expensive to recompute
- Microservice architecture (decoupled services)
- Need guaranteed delivery through restarts

❌ **Don't use when:**
- Results are small and ephemeral
- Using `removeOnComplete: true` (no persistence)
- Immediate consumption via events is sufficient
- Simplicity is more important

## Error Handling

If sending to results queue fails:
- ❌ Error callback is called (if configured)
- ✅ Original job still completes successfully
- ✅ Result is still available in `job.returnvalue`
- ⚠️  Result won't be in results queue (no automatic retry)

## Manual Equivalent

You can achieve the same result manually:

```go
worker.Process(func(job *bullmq.Job) (interface{}, error) {
    result := processJob(job.Data)

    // Manually send to results queue
    resultsQueue := bullmq.NewQueue("results", redisClient)
    resultsQueue.Add(ctx, "process-result", map[string]interface{}{
        "jobId": job.ID,
        "result": result,
    }, bullmq.JobOptions{Attempts: 5})

    return result, nil
})
```

`ProcessWithResults()` is just a convenience wrapper!

## Learn More

- [BullMQ Returning Job Data](https://docs.bullmq.io/guide/returning-job-data)
- [BullMQ Going to Production](https://docs.bullmq.io/guide/going-to-production)
- [BullMQ GitHub Repository](https://github.com/taskforcesh/bullmq)
