package main

import (
	"context"
	"fmt"
	"log"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Connect to Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer redisClient.Close()

	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Create queue
	queue := bullmq.NewQueue("email-queue", redisClient)

	// 1. Get job counts
	fmt.Println("=== Job Counts ===")
	counts, err := queue.GetJobCounts(ctx)
	if err != nil {
		log.Fatalf("Failed to get job counts: %v", err)
	}

	fmt.Printf("Waiting: %d\n", counts.Waiting)
	fmt.Printf("Prioritized: %d\n", counts.Prioritized)
	fmt.Printf("Delayed: %d\n", counts.Delayed)
	fmt.Printf("Active: %d\n", counts.Active)
	fmt.Printf("Completed: %d\n", counts.Completed)
	fmt.Printf("Failed: %d\n", counts.Failed)

	// 2. Pause queue
	fmt.Println("\n=== Pausing Queue ===")
	if err := queue.Pause(ctx); err != nil {
		log.Fatalf("Failed to pause queue: %v", err)
	}
	fmt.Println("✓ Queue paused")

	isPaused, _ := queue.IsPaused(ctx)
	fmt.Printf("Is paused: %v\n", isPaused)

	// 3. Resume queue
	fmt.Println("\n=== Resuming Queue ===")
	if err := queue.Resume(ctx); err != nil {
		log.Fatalf("Failed to resume queue: %v", err)
	}
	fmt.Println("✓ Queue resumed")

	isPaused, _ = queue.IsPaused(ctx)
	fmt.Printf("Is paused: %v\n", isPaused)

	fmt.Println("\n✅ Queue management operations completed!")
}
