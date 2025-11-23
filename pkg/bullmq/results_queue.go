package bullmq

import (
	"context"
	"time"
)

// ResultsQueueConfig configures automatic results queue forwarding.
//
// NOTE: This is an application-level pattern, not a BullMQ protocol feature.
// The results queue pattern provides reliable result persistence by sending
// job results to a dedicated queue for downstream processing (e.g., database writes).
//
// See: https://docs.bullmq.io/guide/returning-job-data
type ResultsQueueConfig struct {
	// QueueName is the name of the results queue
	QueueName string

	// Options for jobs added to the results queue
	Options JobOptions

	// OnError is called when sending to results queue fails (optional)
	// The original job still completes successfully with returnvalue set
	OnError func(jobID string, err error)
}

// DefaultResultsQueueConfig provides sensible defaults for results queue
var DefaultResultsQueueConfig = ResultsQueueConfig{
	Options: JobOptions{
		Attempts:         5,    // Retry failed result processing
		RemoveOnComplete: true, // Clean up after processing
		Backoff: BackoffConfig{
			Type:  "exponential",
			Delay: 1000,
		},
	},
	OnError: nil, // No-op by default
}

// sendToResultsQueue sends job result to the configured results queue
// with rich metadata for downstream processing.
//
// This method is called automatically after successful job completion when
// results queue is configured via ProcessWithResults() or WorkerOptions.
func (w *Worker) sendToResultsQueue(job *Job, result interface{}, duration time.Duration) error {
	if w.resultsQueue == nil || w.resultsQueueConfig == nil {
		return nil // Results queue not configured
	}

	ctx := context.Background()

	// Add result to results queue with metadata
	_, err := w.resultsQueue.Add(ctx, "process-result", map[string]interface{}{
		// Core data
		"jobId":     job.ID,
		"queueName": w.queueName,
		"result":    result,

		// Metadata for observability
		"timestamp":   time.Now().Unix(),
		"processTime": duration.Milliseconds(), // Processing duration in ms
		"attempt":     job.AttemptsMade,        // Which attempt succeeded
		"workerId":    w.opts.WorkerID,         // Which worker processed it
	}, w.resultsQueueConfig.Options)

	// Call error handler if configured
	if err != nil && w.resultsQueueConfig.OnError != nil {
		w.resultsQueueConfig.OnError(job.ID, err)
	}

	// Don't fail the job - result is still stored in returnvalue
	return err
}
