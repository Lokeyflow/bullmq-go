package bullmq

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/redis/go-redis/v9"
)

// ErrorCategory classifies errors for retry logic
type ErrorCategory int

const (
	ErrorCategoryPermanent ErrorCategory = iota
	ErrorCategoryTransient
)

// TransientError indicates a temporary failure (should retry)
type TransientError struct {
	Err error
	Msg string
}

func (e *TransientError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("transient error: %s: %v", e.Msg, e.Err)
	}
	return fmt.Sprintf("transient error: %v", e.Err)
}

func (e *TransientError) Unwrap() error {
	return e.Err
}

// PermanentError indicates a permanent failure (should not retry)
type PermanentError struct {
	Err error
	Msg string
}

func (e *PermanentError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("permanent error: %s: %v", e.Msg, e.Err)
	}
	return fmt.Sprintf("permanent error: %v", e.Err)
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

// ValidationError indicates invalid input
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
}

// CategorizeError determines if an error is transient or permanent
func CategorizeError(err error) ErrorCategory {
	if err == nil {
		return ErrorCategoryPermanent
	}

	// Check for explicit error type wrappers
	var transientErr *TransientError
	if errors.As(err, &transientErr) {
		return ErrorCategoryTransient
	}

	var permanentErr *PermanentError
	if errors.As(err, &permanentErr) {
		return ErrorCategoryPermanent
	}

	// Network errors → transient
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ErrorCategoryTransient
	}

	// Context errors → transient
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrorCategoryTransient
	}

	// Redis errors
	if errors.Is(err, redis.Nil) {
		return ErrorCategoryPermanent // Key not found (intentional deletion)
	}

	errStr := err.Error()

	// Redis connection errors → transient
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "LOADING") ||
		strings.Contains(errStr, "READONLY") {
		return ErrorCategoryTransient
	}

	// Validation errors → permanent
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		return ErrorCategoryPermanent
	}

	// Default: treat as permanent (fail fast)
	return ErrorCategoryPermanent
}
