package bullmq

import (
	"github.com/lokeyflow/bullmq-go/pkg/bullmq/scripts"
	"github.com/redis/go-redis/v9"
)

// Queue manages job submission and queue operations
type Queue struct {
	name         string
	redisClient  redis.Cmdable
	keyBuilder   *KeyBuilder
	scripts      *scripts.ScriptLoader
	eventEmitter *EventEmitter
}

// JobCounts represents queue statistics
type JobCounts struct {
	Waiting     int64
	Active      int64
	Completed   int64
	Failed      int64
	Delayed     int64
	Prioritized int64
}

// NewQueue creates a new queue instance
// Accepts both *redis.Client and *redis.ClusterClient via redis.Cmdable interface
func NewQueue(name string, redisClient redis.Cmdable) *Queue {
	scriptLoader := scripts.NewScriptLoader(redisClient)
	scriptLoader.LoadAll()

	return &Queue{
		name:         name,
		redisClient:  redisClient,
		keyBuilder:   NewKeyBuilder(name, redisClient),
		scripts:      scriptLoader,
		eventEmitter: NewEventEmitter(name, redisClient, 10000),
	}
}
