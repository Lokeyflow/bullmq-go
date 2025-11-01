# BullMQ Go Examples

This directory contains example applications demonstrating how to use the bullmq-go library.

## Examples

### 1. Complete Example (`complete/`)
A full demonstration showing:
- Email worker with retry logic
- Image processing worker with progress tracking
- Job addition and management
- Graceful shutdown

**Run**:
```bash
cd examples/complete
go run main.go
```

### 2. Worker Example (`worker/`) 
Demonstrates the Worker API with:
- Job processing
- Progress tracking
- Logging
- Retry logic with exponential backoff
- Transient vs permanent error handling

**Run**:
```bash
# Terminal 1: Start worker
cd examples/worker
go run main.go

# Terminal 2: Add jobs (use producer example)
cd examples/producer
go run main.go
```

### 3. Producer Example (`producer/`)
Shows different ways to add jobs:
- Simple FIFO jobs
- Priority jobs
- Delayed/scheduled jobs
- Jobs with custom retry settings
- Batch job addition

**Run**:
```bash
cd examples/producer
go run main.go
```

## Prerequisites

**Redis Server**: Make sure Redis is running on `localhost:6379`

```bash
# Docker
docker run -d -p 6379:6379 redis:7-alpine

# Or Rancher Desktop
kubectl run redis --image=redis:7-alpine --port=6379
kubectl port-forward pod/redis 6379:6379
```

## Common Workflows

### Workflow 1: Basic Producer-Consumer
```bash
# 1. Start Redis
docker run -d -p 6379:6379 redis:7-alpine

# 2. Start worker
cd examples/worker && go run main.go

# 3. In another terminal, add jobs
cd examples/producer && go run main.go
```

### Workflow 2: Monitor Queue State
```bash
# Connect to Redis CLI
redis-cli

# Check queue stats
LLEN bull:email-queue:wait       # Waiting jobs
LLEN bull:email-queue:active     # Currently processing
ZCARD bull:email-queue:completed # Completed jobs
ZCARD bull:email-queue:failed    # Failed jobs

# View job details
HGETALL bull:email-queue:<job-id>

# View job logs
LRANGE bull:email-queue:<job-id>:logs 0 -1
```

## Features Demonstrated

| Feature | Example |
|---------|---------|
| Job Processing | worker/, complete/ |
| Priority Queues | producer/ |
| Delayed Jobs | producer/ |
| Retry Logic | worker/ |
| Progress Tracking | worker/ |
| Job Logging | worker/ |
| Stalled Detection | worker/ (automatic) |
| Graceful Shutdown | worker/, complete/ |

## Next Steps

- Check out the [main README](../README.md) for API documentation
- Read [REDIS_TESTS_PASSED.md](../REDIS_TESTS_PASSED.md) for test results
- Explore integration tests in `tests/integration/`
