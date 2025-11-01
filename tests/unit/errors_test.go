package unit

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestCategorizeError_TransientErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "Explicit TransientError",
			err:  &bullmq.TransientError{Err: errors.New("network timeout")},
		},
		{
			name: "Context DeadlineExceeded",
			err:  context.DeadlineExceeded,
		},
		{
			name: "Context Canceled",
			err:  context.Canceled,
		},
		{
			name: "Network timeout",
			err:  &net.OpError{Op: "dial", Err: errors.New("timeout")},
		},
		{
			name: "Connection refused",
			err:  errors.New("connection refused"),
		},
		{
			name: "I/O timeout",
			err:  errors.New("i/o timeout"),
		},
		{
			name: "Redis LOADING",
			err:  errors.New("LOADING Redis is loading the dataset"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			category := bullmq.CategorizeError(tt.err)
			assert.Equal(t, bullmq.ErrorCategoryTransient, category,
				"Error %v should be categorized as transient", tt.err)
		})
	}
}

func TestCategorizeError_PermanentErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "Explicit PermanentError",
			err:  &bullmq.PermanentError{Err: errors.New("validation failed")},
		},
		{
			name: "Redis Nil (key not found)",
			err:  redis.Nil,
		},
		{
			name: "Validation error",
			err:  &bullmq.ValidationError{Field: "email", Message: "invalid format"},
		},
		{
			name: "Generic error",
			err:  errors.New("something went wrong"),
		},
		{
			name: "Nil error",
			err:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			category := bullmq.CategorizeError(tt.err)
			assert.Equal(t, bullmq.ErrorCategoryPermanent, category,
				"Error %v should be categorized as permanent", tt.err)
		})
	}
}

func TestTransientError_Unwrap(t *testing.T) {
	baseErr := errors.New("base error")
	transientErr := &bullmq.TransientError{Err: baseErr, Msg: "retry me"}

	assert.ErrorIs(t, transientErr, baseErr)
	assert.Contains(t, transientErr.Error(), "transient error")
	assert.Contains(t, transientErr.Error(), "retry me")
}

func TestPermanentError_Unwrap(t *testing.T) {
	baseErr := errors.New("base error")
	permanentErr := &bullmq.PermanentError{Err: baseErr, Msg: "do not retry"}

	assert.ErrorIs(t, permanentErr, baseErr)
	assert.Contains(t, permanentErr.Error(), "permanent error")
	assert.Contains(t, permanentErr.Error(), "do not retry")
}
