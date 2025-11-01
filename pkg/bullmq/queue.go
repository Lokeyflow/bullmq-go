package bullmq

import (
	"github.com/Lokeyflow/bullmq-go/pkg/bullmq/scripts"
	"github.com/redis/go-redis/v9"
)

// Queue manages job submission and queue operations
type Queue struct {
	name        string
	redisClient *redis.Client
	keyBuilder  *KeyBuilder
	scripts     *scripts.ScriptLoader
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
func NewQueue(name string, redisClient *redis.Client) *Queue {
	scriptLoader := scripts.NewScriptLoader(redisClient)
	scriptLoader.LoadAll()

	return &Queue{
		name:        name,
		redisClient: redisClient,
		keyBuilder:  NewKeyBuilder(name),
		scripts:     scriptLoader,
	}
}
