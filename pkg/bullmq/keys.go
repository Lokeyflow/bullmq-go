package bullmq

import "fmt"

// KeyBuilder generates Redis keys with conditional hash tags for cluster compatibility.
//
// Hash tags {queue-name} are used ONLY in Redis Cluster mode to ensure all keys for a queue
// hash to the same slot (required for multi-key Lua scripts). In single-instance Redis mode,
// hash tags are omitted to match Node.js BullMQ default behavior for cross-language compatibility.
type KeyBuilder struct {
	queueName   string
	useHashTags bool // Auto-detected from Redis client type, or explicitly set
}

// NewKeyBuilder creates a new key builder for a queue with auto-detected Redis mode.
//
// The key format is automatically determined based on the Redis client type:
//   - redis.ClusterClient → uses hash tags: bull:{queue-name}:wait
//   - redis.Client → no hash tags: bull:queue-name:wait
//
// This ensures cross-language compatibility with Node.js BullMQ while maintaining
// Redis Cluster support.
func NewKeyBuilder(queueName string, client interface{}) *KeyBuilder {
	return &KeyBuilder{
		queueName:   queueName,
		useHashTags: IsRedisCluster(client),
	}
}

// NewKeyBuilderWithHashTags creates a key builder with explicit hash tag control.
//
// Use this when you need to force hash tags on single-instance Redis (e.g., testing
// cluster behavior) or override auto-detection for special cases.
func NewKeyBuilderWithHashTags(queueName string, useHashTags bool) *KeyBuilder {
	return &KeyBuilder{
		queueName:   queueName,
		useHashTags: useHashTags,
	}
}

// Wait returns the key for the wait queue (FIFO, priority=0)
func (kb *KeyBuilder) Wait() string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:wait", kb.queueName)
	}
	return fmt.Sprintf("bull:%s:wait", kb.queueName)
}

// Prioritized returns the key for the prioritized queue (ZSET, priority>0)
func (kb *KeyBuilder) Prioritized() string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:prioritized", kb.queueName)
	}
	return fmt.Sprintf("bull:%s:prioritized", kb.queueName)
}

// Delayed returns the key for the delayed queue (ZSET, scheduled jobs)
func (kb *KeyBuilder) Delayed() string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:delayed", kb.queueName)
	}
	return fmt.Sprintf("bull:%s:delayed", kb.queueName)
}

// Active returns the key for the active jobs list
func (kb *KeyBuilder) Active() string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:active", kb.queueName)
	}
	return fmt.Sprintf("bull:%s:active", kb.queueName)
}

// Completed returns the key for the completed jobs sorted set
func (kb *KeyBuilder) Completed() string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:completed", kb.queueName)
	}
	return fmt.Sprintf("bull:%s:completed", kb.queueName)
}

// Failed returns the key for the failed jobs sorted set
func (kb *KeyBuilder) Failed() string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:failed", kb.queueName)
	}
	return fmt.Sprintf("bull:%s:failed", kb.queueName)
}

// Events returns the key for the events stream
func (kb *KeyBuilder) Events() string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:events", kb.queueName)
	}
	return fmt.Sprintf("bull:%s:events", kb.queueName)
}

// Meta returns the key for queue metadata (paused, rate limits)
func (kb *KeyBuilder) Meta() string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:meta", kb.queueName)
	}
	return fmt.Sprintf("bull:%s:meta", kb.queueName)
}

// Job returns the key for a specific job hash
func (kb *KeyBuilder) Job(jobID string) string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:%s", kb.queueName, jobID)
	}
	return fmt.Sprintf("bull:%s:%s", kb.queueName, jobID)
}

// Lock returns the key for a job's lock
func (kb *KeyBuilder) Lock(jobID string) string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:%s:lock", kb.queueName, jobID)
	}
	return fmt.Sprintf("bull:%s:%s:lock", kb.queueName, jobID)
}

// Logs returns the key for a job's logs list
func (kb *KeyBuilder) Logs(jobID string) string {
	if kb.useHashTags {
		return fmt.Sprintf("bull:{%s}:%s:logs", kb.queueName, jobID)
	}
	return fmt.Sprintf("bull:%s:%s:logs", kb.queueName, jobID)
}
