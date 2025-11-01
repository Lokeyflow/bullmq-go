package bullmq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq/scripts"
	"github.com/redis/go-redis/v9"
)

const (
	// DefaultMaxLogs is the default maximum number of logs to keep per job
	DefaultMaxLogs = 1000
)

// LogEntry represents a single log entry for a job
type LogEntry struct {
	Timestamp int64  `json:"timestamp"` // Unix timestamp in milliseconds
	Message   string `json:"message"`
}

// LogManager handles atomic log operations using Lua scripts
type LogManager struct {
	queueName    string
	redisClient  redis.Cmdable
	scriptLoader *scripts.ScriptLoader
	maxLogs      int
}

// NewLogManager creates a new log manager
func NewLogManager(queueName string, redisClient redis.Cmdable, maxLogs int) *LogManager {
	if maxLogs <= 0 {
		maxLogs = DefaultMaxLogs
	}

	scriptLoader := scripts.NewScriptLoader(redisClient)
	scriptLoader.LoadAll()

	return &LogManager{
		queueName:    queueName,
		redisClient:  redisClient,
		scriptLoader: scriptLoader,
		maxLogs:      maxLogs,
	}
}

// AddLog atomically adds a log entry to a job and trims to max logs
// Uses addLog Lua script for atomicity
//
// Returns:
//   - Total log count after addition (capped at maxLogs)
//   - -1: Job not found
func (lm *LogManager) AddLog(ctx context.Context, jobID string, message string) (int64, error) {
	kb := NewKeyBuilder(lm.queueName, lm.redisClient)

	// Create log entry
	logEntry := LogEntry{
		Timestamp: time.Now().UnixMilli(),
		Message:   message,
	}

	// Serialize to JSON (BullMQ stores logs as JSON strings)
	logJSON, err := json.Marshal(logEntry)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal log entry: %w", err)
	}

	// KEYS
	keys := []string{
		kb.Job(jobID),  // KEYS[1]: Job hash key
		kb.Logs(jobID), // KEYS[2]: Logs list key
	}

	// ARGV
	args := []interface{}{
		jobID,       // ARGV[1]: Job ID
		string(logJSON), // ARGV[2]: Log entry (JSON string)
		lm.maxLogs,  // ARGV[3]: keepLogs (max logs to retain)
	}

	// Execute Lua script
	result, err := lm.scriptLoader.Run(ctx, scripts.ScriptAddLog, keys, args...).Result()
	if err != nil {
		return 0, fmt.Errorf("addLog script failed: %w", err)
	}

	// Parse result
	logCount, ok := result.(int64)
	if !ok {
		return 0, fmt.Errorf("unexpected result type from addLog: %T", result)
	}

	if logCount == -1 {
		return -1, fmt.Errorf("job not found: %s", jobID)
	}

	return logCount, nil
}

// GetLogs retrieves all logs for a job
func (lm *LogManager) GetLogs(ctx context.Context, jobID string) ([]LogEntry, error) {
	kb := NewKeyBuilder(lm.queueName, lm.redisClient)
	logsKey := kb.Logs(jobID)

	// Get all logs from list
	logStrings, err := lm.redisClient.LRange(ctx, logsKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get logs: %w", err)
	}

	// Parse log entries
	logs := make([]LogEntry, 0, len(logStrings))
	for _, logStr := range logStrings {
		var entry LogEntry
		if err := json.Unmarshal([]byte(logStr), &entry); err != nil {
			// Skip malformed entries
			continue
		}
		logs = append(logs, entry)
	}

	return logs, nil
}
