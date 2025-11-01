package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Connect to Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // No password by default
		DB:       0,  // Default DB
	})
	defer redisClient.Close()

	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	// 2. Create queue
	queue := bullmq.NewQueue("email-queue", redisClient)

	// 3. Add basic job
	fmt.Println("=== Adding Basic Job ===")
	job1, err := queue.Add(ctx, "send-email", map[string]interface{}{
		"to":      "user@example.com",
		"subject": "Welcome!",
		"body":    "Thank you for signing up.",
	}, bullmq.DefaultJobOptions)

	if err != nil {
		log.Fatalf("Failed to add job: %v", err)
	}
	fmt.Printf("✓ Job added: %s\n", job1.ID)

	// 4. Add job with priority
	fmt.Println("\n=== Adding Priority Job ===")
	job2, err := queue.Add(ctx, "send-email", map[string]interface{}{
		"to":      "vip@example.com",
		"subject": "VIP Notification",
		"body":    "You have a new message.",
	}, bullmq.JobOptions{
		Priority: 10,
		Attempts: 3,
		Backoff: bullmq.BackoffConfig{
			Type:  "exponential",
			Delay: 1000,
		},
	})

	if err != nil {
		log.Fatalf("Failed to add priority job: %v", err)
	}
	fmt.Printf("✓ Priority job added: %s (priority: %d)\n", job2.ID, job2.Opts.Priority)

	// 5. Add delayed job
	fmt.Println("\n=== Adding Delayed Job ===")
	job3, err := queue.Add(ctx, "send-email", map[string]interface{}{
		"to":      "user@example.com",
		"subject": "Reminder",
		"body":    "Don't forget to check your account!",
	}, bullmq.JobOptions{
		Delay:    5 * time.Minute,
		Attempts: 3,
		Backoff:  bullmq.BackoffConfig{Type: "exponential", Delay: 1000},
	})

	if err != nil {
		log.Fatalf("Failed to add delayed job: %v", err)
	}
	fmt.Printf("✓ Delayed job added: %s (delay: 5m)\n", job3.ID)

	// 6. Get queue counts
	fmt.Println("\n=== Queue Statistics ===")
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

	fmt.Println("\n✅ All jobs submitted successfully!")
	fmt.Println("💡 Run the worker example to process these jobs")
}
