package bullmq

import (
	"context"
	"sync"
	"time"
)

// HeartbeatManager manages lock extensions for active jobs
type HeartbeatManager struct {
	worker       *Worker
	activeLocks  map[string]context.CancelFunc
	mu           sync.RWMutex
	stopChan     chan struct{}
	failureCount uint64
}

// NewHeartbeatManager creates a new heartbeat manager
func NewHeartbeatManager(worker *Worker) *HeartbeatManager {
	return &HeartbeatManager{
		worker:      worker,
		activeLocks: make(map[string]context.CancelFunc),
		stopChan:    make(chan struct{}),
	}
}

// StartHeartbeat starts heartbeat for a specific job
func (hm *HeartbeatManager) StartHeartbeat(ctx context.Context, jobID string, lockToken LockToken) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Cancel existing heartbeat if any
	if cancel, exists := hm.activeLocks[jobID]; exists {
		cancel()
	}

	// Create cancellable context for this job's heartbeat
	heartbeatCtx, cancel := context.WithCancel(ctx)
	hm.activeLocks[jobID] = cancel

	// Start heartbeat loop
	go hm.heartbeatLoop(heartbeatCtx, jobID, lockToken)
}

// StopHeartbeat stops heartbeat for a specific job
func (hm *HeartbeatManager) StopHeartbeat(jobID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if cancel, exists := hm.activeLocks[jobID]; exists {
		cancel()
		delete(hm.activeLocks, jobID)
	}
}

// heartbeatLoop extends lock periodically
func (hm *HeartbeatManager) heartbeatLoop(ctx context.Context, jobID string, lockToken LockToken) {
	ticker := time.NewTicker(hm.worker.opts.HeartbeatInterval)
	defer ticker.Stop()
	defer hm.StopHeartbeat(jobID)

	kb := NewKeyBuilder(hm.worker.queueName)
	lockKey := kb.Lock(jobID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-hm.stopChan:
			return
		case <-ticker.C:
			// Extend lock if it still matches our token
			result, err := hm.extendLock(ctx, lockKey, lockToken)
			if err != nil || result == 0 {
				// Lock extension failed (job completed, lock expired, or stolen)
				// Log but continue processing - stalled checker will recover if needed
				hm.failureCount++
			}
		}
	}
}

// extendLock extends lock TTL if token matches (atomic)
func (hm *HeartbeatManager) extendLock(ctx context.Context, lockKey string, expectedToken LockToken) (int64, error) {
	// Lua script for atomic compare-and-extend
	script := `
		local lockKey = KEYS[1]
		local expectedToken = ARGV[1]
		local ttl = tonumber(ARGV[2])

		local currentToken = redis.call('GET', lockKey)
		if currentToken == expectedToken then
			return redis.call('EXPIRE', lockKey, ttl)
		else
			return 0
		end
	`

	result, err := hm.worker.redisClient.Eval(ctx, script,
		[]string{lockKey},
		expectedToken.String(),
		int(hm.worker.opts.LockDuration.Seconds()),
	).Int64()

	return result, err
}

// Stop stops all heartbeats
func (hm *HeartbeatManager) Stop() {
	close(hm.stopChan)

	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Cancel all active heartbeats
	for _, cancel := range hm.activeLocks {
		cancel()
	}
	hm.activeLocks = make(map[string]context.CancelFunc)
}

// GetFailureCount returns total heartbeat failures
func (hm *HeartbeatManager) GetFailureCount() uint64 {
	return hm.failureCount
}
