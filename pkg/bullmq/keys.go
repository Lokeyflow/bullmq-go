package bullmq

import "fmt"

// KeyBuilder generates Redis keys with hash tags for cluster compatibility
type KeyBuilder struct {
	queueName string
}

// NewKeyBuilder creates a new key builder for a queue
func NewKeyBuilder(queueName string) *KeyBuilder {
	return &KeyBuilder{queueName: queueName}
}

// Wait returns the key for the wait queue (FIFO, priority=0)
func (kb *KeyBuilder) Wait() string {
	return fmt.Sprintf("bull:{%s}:wait", kb.queueName)
}

// Prioritized returns the key for the prioritized queue (ZSET, priority>0)
func (kb *KeyBuilder) Prioritized() string {
	return fmt.Sprintf("bull:{%s}:prioritized", kb.queueName)
}

// Delayed returns the key for the delayed queue (ZSET, scheduled jobs)
func (kb *KeyBuilder) Delayed() string {
	return fmt.Sprintf("bull:{%s}:delayed", kb.queueName)
}

// Active returns the key for the active jobs list
func (kb *KeyBuilder) Active() string {
	return fmt.Sprintf("bull:{%s}:active", kb.queueName)
}

// Completed returns the key for the completed jobs sorted set
func (kb *KeyBuilder) Completed() string {
	return fmt.Sprintf("bull:{%s}:completed", kb.queueName)
}

// Failed returns the key for the failed jobs sorted set
func (kb *KeyBuilder) Failed() string {
	return fmt.Sprintf("bull:{%s}:failed", kb.queueName)
}

// Events returns the key for the events stream
func (kb *KeyBuilder) Events() string {
	return fmt.Sprintf("bull:{%s}:events", kb.queueName)
}

// Meta returns the key for queue metadata (paused, rate limits)
func (kb *KeyBuilder) Meta() string {
	return fmt.Sprintf("bull:{%s}:meta", kb.queueName)
}

// Job returns the key for a specific job hash
func (kb *KeyBuilder) Job(jobID string) string {
	return fmt.Sprintf("bull:{%s}:%s", kb.queueName, jobID)
}

// Lock returns the key for a job's lock
func (kb *KeyBuilder) Lock(jobID string) string {
	return fmt.Sprintf("bull:{%s}:%s:lock", kb.queueName, jobID)
}

// Logs returns the key for a job's logs list
func (kb *KeyBuilder) Logs(jobID string) string {
	return fmt.Sprintf("bull:{%s}:%s:logs", kb.queueName, jobID)
}
