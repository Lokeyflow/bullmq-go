package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
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

	// 2. Create worker with options
	worker := bullmq.NewWorker("email-queue", redisClient, bullmq.WorkerOptions{
		Concurrency:          5,               // Process up to 5 jobs concurrently
		LockDuration:         30 * time.Second, // Lock job for 30 seconds
		HeartbeatInterval:    15 * time.Second, // Extend lock every 15 seconds
		StalledCheckInterval: 30 * time.Second, // Check for stalled jobs every 30s
		MaxAttempts:          3,                // Retry failed jobs up to 3 times
		BackoffDelay:         time.Second,      // Base backoff delay: 1 second
		WorkerID:             "",               // Auto-generate worker ID
	})

	fmt.Printf("🚀 Starting worker: %s\n", worker.GetWorkerID())
	fmt.Println("📋 Listening for jobs on queue: email-queue")

	// 3. Define job processor
	worker.Process(func(job *bullmq.Job) error {
		fmt.Printf("\n[%s] Processing job %s: %s\n", time.Now().Format("15:04:05"), job.ID, job.Name)

		// Extract job data
		to := job.Data["to"].(string)
		subject := job.Data["subject"].(string)
		body := job.Data["body"].(string)

		// Simulate processing
		fmt.Printf("  📧 Sending email to %s\n", to)
		fmt.Printf("  📝 Subject: %s\n", subject)
		time.Sleep(2 * time.Second)

		// Simulate email sending
		if err := sendEmail(to, subject, body); err != nil {
			fmt.Printf("  ❌ Failed: %v\n", err)
			return err
		}

		fmt.Printf("  ✅ Email sent successfully!\n")
		return nil
	})

	// 4. Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⏸️  Shutdown signal received, stopping worker...")
		cancel()
	}()

	// 5. Start worker (blocks until context cancelled)
	if err := worker.Start(ctx); err != nil {
		log.Fatalf("Worker failed: %v", err)
	}

	fmt.Println("👋 Worker stopped gracefully")
}

// sendEmail simulates sending an email
func sendEmail(to, subject, body string) error {
	// Replace with actual email sending logic
	// For now, just simulate success
	return nil
}
