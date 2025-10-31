package bullmq

import (
	"encoding/json"
	"fmt"
)

const (
	// MaxJobPayloadSize is the maximum allowed job payload size (10MB)
	MaxJobPayloadSize = 10 * 1024 * 1024 // 10MB
)

// ValidateJobOptions validates job options before submission
func ValidateJobOptions(opts JobOptions) error {
	// Validate priority
	if opts.Priority < 0 {
		return &ValidationError{
			Field:   "priority",
			Message: fmt.Sprintf("must be >= 0, got %d", opts.Priority),
		}
	}

	// Validate delay
	if opts.Delay < 0 {
		return &ValidationError{
			Field:   "delay",
			Message: fmt.Sprintf("must be >= 0, got %v", opts.Delay),
		}
	}

	// Validate attempts
	if opts.Attempts <= 0 {
		return &ValidationError{
			Field:   "attempts",
			Message: fmt.Sprintf("must be > 0, got %d", opts.Attempts),
		}
	}

	// Validate backoff only if specified (non-empty type)
	if opts.Backoff.Type != "" {
		if err := ValidateBackoffConfig(opts.Backoff); err != nil {
			return err
		}
	}

	return nil
}

// ValidateBackoffConfig validates backoff configuration
func ValidateBackoffConfig(backoff BackoffConfig) error {
	// Validate backoff type
	if backoff.Type != "fixed" && backoff.Type != "exponential" {
		return &ValidationError{
			Field:   "backoff.type",
			Message: fmt.Sprintf("must be 'fixed' or 'exponential', got '%s'", backoff.Type),
		}
	}

	// Validate backoff delay
	if backoff.Delay <= 0 {
		return &ValidationError{
			Field:   "backoff.delay",
			Message: fmt.Sprintf("must be > 0, got %d", backoff.Delay),
		}
	}

	return nil
}

// ValidateJobPayloadSize validates that job payload is within size limits
func ValidateJobPayloadSize(job *Job) error {
	// Serialize job to JSON to get actual size
	data, err := json.Marshal(job)
	if err != nil {
		return &ValidationError{
			Field:   "job",
			Message: fmt.Sprintf("failed to serialize: %v", err),
		}
	}

	size := len(data)
	if size > MaxJobPayloadSize {
		return &ValidationError{
			Field: "job",
			Message: fmt.Sprintf(
				"payload size %s exceeds limit of %s",
				formatBytes(size),
				formatBytes(MaxJobPayloadSize),
			),
		}
	}

	return nil
}

// formatBytes formats bytes in human-readable format
func formatBytes(bytes int) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
