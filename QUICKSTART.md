# BullMQ-Go Quick Start

Get started with BullMQ-Go in 5 minutes.

## Prerequisites

- Go 1.21 or higher
- Redis 6.0 or higher running on localhost:6379

## Installation

```bash
go get github.com/lokeyflow/bullmq-go/pkg/bullmq
```

## Step 1: Start Redis

Choose one method:

```bash
# Docker
docker run -d -p 6379:6379 redis:7-alpine

# Rancher Desktop (nerdctl)
nerdctl run -d -p 6379:6379 redis:7-alpine
```

## Step 2: Create a Producer

**producer.go**
```go
package main

import (
    "context"
    "log"
    "github.com/lokeyflow/bullmq-go/pkg/bullmq"
    "github.com/redis/go-redis/v9"
)

func main() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    queue := bullmq.NewQueue("tasks", client)

    job, err := queue.Add(context.Background(), "process-data", map[string]interface{}{
        "input": "hello world",
    }, bullmq.DefaultJobOptions)

    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Job added: %s", job.ID)
}
```

## Step 3: Create a Worker

**worker.go**
```go
package main

import (
    "context"
    "fmt"
    "log"
    "github.com/lokeyflow/bullmq-go/pkg/bullmq"
    "github.com/redis/go-redis/v9"
)

func main() {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer client.Close()

    worker := bullmq.NewWorker("tasks", client, bullmq.DefaultWorkerOptions)

    worker.Process(func(job *bullmq.Job) error {
        input := job.Data["input"].(string)
        fmt.Printf("Processing: %s\n", input)
        return nil
    })

    log.Println("Worker started...")
    worker.Start(context.Background())
}
```

## Step 4: Run It

```bash
# Terminal 1: Start worker
go run worker.go

# Terminal 2: Add jobs
go run producer.go
```

You should see output like:
```
Worker started...
Processing: hello world
```

## Next Steps

- [Full Examples](examples/)
- [API Documentation](https://pkg.go.dev/github.com/lokeyflow/bullmq-go)
- [Advanced Features](README.md#advanced-features)
