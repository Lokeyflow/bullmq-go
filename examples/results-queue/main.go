package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx := context.Background()

	// Connect to Redis
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Println("✅ Connected to Redis")

	// EXAMPLE 1: Explicit Mode - ProcessWithResults()
	log.Println("\n📹 Starting video processing worker (explicit mode)...")
	videoWorker := bullmq.NewWorker("video-queue", rdb, bullmq.WorkerOptions{
		Concurrency: 5,
	})

	videoWorker.ProcessWithResults("results", func(job *bullmq.Job) (interface{}, error) {
		videoURL := job.Data["videoURL"].(string)
		log.Printf("  🎬 Processing video: %s (job %s)", videoURL, job.ID)

		// Simulate video processing
		time.Sleep(2 * time.Second)

		// Return result
		result := map[string]interface{}{
			"outputURL": fmt.Sprintf("https://cdn.example.com/processed/%s.mp4", job.ID),
			"duration":  123.45,
			"format":    "mp4",
			"size":      1024 * 1024 * 50, // 50MB
		}

		log.Printf("  ✅ Video processed: %s", result["outputURL"])
		return result, nil
	}, bullmq.ResultsQueueConfig{
		OnError: func(jobID string, err error) {
			log.Printf("  ❌ Failed to send job %s to results queue: %v", jobID, err)
		},
	})

	// EXAMPLE 2: Implicit Mode - WorkerOptions
	log.Println("📧 Starting email worker (implicit mode)...")
	emailWorker := bullmq.NewWorker("email-queue", rdb, bullmq.WorkerOptions{
		Concurrency: 10,
		ResultsQueue: &bullmq.ResultsQueueConfig{
			QueueName: "results",
			OnError: func(jobID string, err error) {
				log.Printf("  ❌ Results queue error for %s: %v", jobID, err)
			},
		},
	})

	emailWorker.Process(func(job *bullmq.Job) (interface{}, error) {
		to := job.Data["to"].(string)
		subject := job.Data["subject"].(string)

		log.Printf("  📨 Sending email to %s: %s", to, subject)

		// Simulate email sending
		time.Sleep(500 * time.Millisecond)

		result := map[string]interface{}{
			"sent":      true,
			"messageId": fmt.Sprintf("msg-%s", job.ID),
			"timestamp": time.Now().Unix(),
		}

		log.Printf("  ✅ Email sent: %s", result["messageId"])
		return result, nil
	})

	// EXAMPLE 3: Results Worker - Processes results from ALL queues
	log.Println("💾 Starting results storage worker...")
	resultsWorker := bullmq.NewWorker("results", rdb, bullmq.WorkerOptions{
		Concurrency: 5,
	})

	resultsWorker.Process(func(job *bullmq.Job) (interface{}, error) {
		jobID := job.Data["jobId"].(string)
		queueName := job.Data["queueName"].(string)
		result := job.Data["result"]
		processTime := job.Data["processTime"]
		attempt := job.Data["attempt"]
		workerID := job.Data["workerId"]

		log.Printf("  💾 Storing result: job=%s queue=%s duration=%vms attempt=%v worker=%s",
			jobID, queueName, processTime, attempt, workerID)

		// Simulate database storage
		time.Sleep(100 * time.Millisecond)

		// In real application, you would:
		// db.SaveResult(jobID, result)
		// notifications.SendWebhook(result)
		// analytics.TrackCompletion(result)

		fmt.Printf("     📊 Result data: %+v\n", result)

		return map[string]interface{}{
			"stored": true,
			"at":     time.Now().Unix(),
		}, nil
	})

	// Create queues for adding test jobs
	videoQueue := bullmq.NewQueue("video-queue", rdb)
	emailQueue := bullmq.NewQueue("email-queue", rdb)

	// Add some test jobs
	log.Println("\n📝 Adding test jobs...")

	// Add video jobs
	for i := 1; i <= 3; i++ {
		job, err := videoQueue.Add(ctx, "process-video", map[string]interface{}{
			"videoURL": fmt.Sprintf("https://example.com/video%d.mp4", i),
		}, bullmq.JobOptions{Attempts: 3})

		if err != nil {
			log.Printf("Failed to add video job: %v", err)
		} else {
			log.Printf("  ✅ Added video job: %s", job.ID)
		}
	}

	// Add email jobs
	for i := 1; i <= 5; i++ {
		job, err := emailQueue.Add(ctx, "send-email", map[string]interface{}{
			"to":      fmt.Sprintf("user%d@example.com", i),
			"subject": fmt.Sprintf("Test Email #%d", i),
			"body":    "This is a test email from bullmq-go",
		}, bullmq.JobOptions{Attempts: 3})

		if err != nil {
			log.Printf("Failed to add email job: %v", err)
		} else {
			log.Printf("  ✅ Added email job: %s", job.ID)
		}
	}

	// Start all workers
	log.Println("\n🚀 Starting all workers...")
	go videoWorker.Start(ctx)
	go emailWorker.Start(ctx)
	go resultsWorker.Start(ctx)

	// Wait for shutdown signal
	log.Println("\n✨ All workers running. Press Ctrl+C to stop...")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("\n🛑 Shutting down gracefully...")
	videoWorker.Stop()
	emailWorker.Stop()
	resultsWorker.Stop()

	log.Println("👋 Shutdown complete")
}
