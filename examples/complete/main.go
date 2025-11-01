package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

// EmailJob represents an email sending job
type EmailJob struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// ImageProcessingJob represents an image processing job
type ImageProcessingJob struct {
	ImageURL   string   `json:"image_url"`
	Operations []string `json:"operations"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup Redis connection
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})
	defer rdb.Close()

	// Test Redis connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("❌ Failed to connect to Redis: %v", err)
	}
	log.Println("✅ Connected to Redis")

	// Start workers in background
	log.Println("\n🚀 Starting workers...")
	go startEmailWorker(ctx, rdb)
	go startImageWorker(ctx, rdb)

	// Give workers time to start
	time.Sleep(500 * time.Millisecond)

	// Add some jobs
	log.Println("\n📝 Adding jobs to queues...")
	addExampleJobs(ctx, rdb)

	// Wait for interrupt signal
	log.Println("\n⏳ Workers running. Press Ctrl+C to stop...")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\n🛑 Shutting down gracefully...")
	cancel()
	time.Sleep(2 * time.Second)
	log.Println("✅ Shutdown complete")
}

// startEmailWorker processes email jobs with retry on failure
func startEmailWorker(ctx context.Context, rdb *redis.Client) {
	log.Println("   📧 Email worker started")

	// Simulate worker processing
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	processedCount := 0
	failedCount := 0

	for {
		select {
		case <-ctx.Done():
			log.Printf("   📧 Email worker stopped (processed: %d, failed: %d)\n",
				processedCount, failedCount)
			return
		case <-ticker.C:
			// Try to get a job from the queue
			// In real implementation, this would use Worker.Process()
			// For demo purposes, we check Redis directly
			queueKey := "bull:email-queue:wait"
			jobID, err := rdb.RPop(ctx, queueKey).Result()
			if err == redis.Nil {
				// No jobs available
				continue
			} else if err != nil {
				log.Printf("   📧 Error getting job: %v\n", err)
				continue
			}

			// Get job data
			jobKey := "bull:email-queue:" + jobID
			jobDataRaw, err := rdb.HGet(ctx, jobKey, "data").Result()
			if err != nil {
				log.Printf("   📧 Error reading job %s: %v\n", jobID, err)
				continue
			}

			var emailJob EmailJob
			if err := json.Unmarshal([]byte(jobDataRaw), &emailJob); err != nil {
				log.Printf("   📧 Error parsing job %s: %v\n", jobID, err)
				continue
			}

			// Simulate email sending (80% success rate)
			if rand.Float32() < 0.8 {
				log.Printf("   📧 ✅ Sent email to %s: %s\n", emailJob.To, emailJob.Subject)
				processedCount++

				// Mark as completed in Redis
				rdb.ZAdd(ctx, "bull:email-queue:completed", redis.Z{
					Score:  float64(time.Now().Unix()),
					Member: jobID,
				})
				rdb.HSet(ctx, jobKey, "finishedOn", time.Now().UnixMilli())
			} else {
				log.Printf("   📧 ❌ Failed to send email to %s (will retry)\n", emailJob.To)
				failedCount++

				// Move back to wait queue for retry
				rdb.LPush(ctx, queueKey, jobID)

				// Increment attempts
				atm, _ := rdb.HGet(ctx, jobKey, "atm").Int()
				rdb.HSet(ctx, jobKey, "atm", atm+1)

				if atm >= 2 {
					log.Printf("   📧 ⚠️  Max retries reached for job %s\n", jobID)
					rdb.ZAdd(ctx, "bull:email-queue:failed", redis.Z{
						Score:  float64(time.Now().Unix()),
						Member: jobID,
					})
				}
			}
		}
	}
}

// startImageWorker processes image jobs with progress tracking
func startImageWorker(ctx context.Context, rdb *redis.Client) {
	log.Println("   🖼️  Image worker started")

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	processedCount := 0

	for {
		select {
		case <-ctx.Done():
			log.Printf("   🖼️  Image worker stopped (processed: %d)\n", processedCount)
			return
		case <-ticker.C:
			// Try to get a job
			queueKey := "bull:image-queue:wait"
			jobID, err := rdb.RPop(ctx, queueKey).Result()
			if err == redis.Nil {
				continue
			} else if err != nil {
				log.Printf("   🖼️  Error getting job: %v\n", err)
				continue
			}

			// Get job data
			jobKey := "bull:image-queue:" + jobID
			jobDataRaw, err := rdb.HGet(ctx, jobKey, "data").Result()
			if err != nil {
				log.Printf("   🖼️  Error reading job %s: %v\n", jobID, err)
				continue
			}

			var imageJob ImageProcessingJob
			if err := json.Unmarshal([]byte(jobDataRaw), &imageJob); err != nil {
				log.Printf("   🖼️  Error parsing job %s: %v\n", jobID, err)
				continue
			}

			log.Printf("   🖼️  Processing image: %s\n", imageJob.ImageURL)

			// Simulate processing with progress updates
			for i, op := range imageJob.Operations {
				progress := int(float64(i+1) / float64(len(imageJob.Operations)) * 100)
				rdb.HSet(ctx, jobKey, "progress", progress)

				// Add log entry
				logEntry := fmt.Sprintf("Applying operation: %s", op)
				logKey := jobKey + ":logs"
				rdb.RPush(ctx, logKey, logEntry)

				log.Printf("   🖼️  Progress %d%%: %s\n", progress, op)
				time.Sleep(500 * time.Millisecond)
			}

			log.Printf("   🖼️  ✅ Completed image processing: %s\n", imageJob.ImageURL)
			processedCount++

			// Mark as completed
			rdb.ZAdd(ctx, "bull:image-queue:completed", redis.Z{
				Score:  float64(time.Now().Unix()),
				Member: jobID,
			})
			rdb.HSet(ctx, jobKey, "finishedOn", time.Now().UnixMilli())
			rdb.HSet(ctx, jobKey, "progress", 100)
		}
	}
}

// addExampleJobs adds sample jobs to both queues
func addExampleJobs(ctx context.Context, rdb *redis.Client) {
	// Add email jobs
	emailJobs := []EmailJob{
		{
			To:      "alice@example.com",
			Subject: "Welcome to BullMQ Go!",
			Body:    "This is your first email from bullmq-go",
		},
		{
			To:      "bob@example.com",
			Subject: "Weekly Report",
			Body:    "Your weekly analytics report is ready",
		},
		{
			To:      "charlie@example.com",
			Subject: "Password Reset",
			Body:    "Click here to reset your password",
		},
	}

	for i, job := range emailJobs {
		jobID := fmt.Sprintf("email-%d-%d", time.Now().Unix(), i)
		jobKey := "bull:email-queue:" + jobID

		jobDataJSON, _ := json.Marshal(job)

		// Create job hash
		rdb.HSet(ctx, jobKey, map[string]interface{}{
			"id":       jobID,
			"name":     "send-email",
			"data":     string(jobDataJSON),
			"atm":      0,
			"priority": 0,
		})

		// Add to wait queue
		rdb.LPush(ctx, "bull:email-queue:wait", jobID)

		log.Printf("   📧 Added email job: %s → %s\n", job.To, job.Subject)
	}

	// Add image processing jobs
	imageJobs := []ImageProcessingJob{
		{
			ImageURL:   "https://example.com/images/photo1.jpg",
			Operations: []string{"resize", "crop", "watermark", "compress"},
		},
		{
			ImageURL:   "https://example.com/images/photo2.png",
			Operations: []string{"convert-to-webp", "optimize"},
		},
	}

	for i, job := range imageJobs {
		jobID := fmt.Sprintf("image-%d-%d", time.Now().Unix(), i)
		jobKey := "bull:image-queue:" + jobID

		jobDataJSON, _ := json.Marshal(job)

		// Create job hash
		rdb.HSet(ctx, jobKey, map[string]interface{}{
			"id":       jobID,
			"name":     "process-image",
			"data":     string(jobDataJSON),
			"atm":      0,
			"priority": 0,
			"progress": 0,
		})

		// Add to wait queue
		rdb.LPush(ctx, "bull:image-queue:wait", jobID)

		log.Printf("   🖼️  Added image job: %s (%d operations)\n",
			job.ImageURL, len(job.Operations))
	}

	log.Printf("\n✅ Added %d email jobs and %d image jobs\n",
		len(emailJobs), len(imageJobs))
}
