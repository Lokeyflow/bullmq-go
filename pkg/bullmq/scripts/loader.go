package scripts

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"sync"

	"github.com/redis/go-redis/v9"
)

// ScriptLoader manages Lua scripts with SHA1 caching for EVALSHA optimization
type ScriptLoader struct {
	client      *redis.Client
	scriptCache map[string]*redis.Script
	mu          sync.RWMutex
}

// NewScriptLoader creates a new script loader
func NewScriptLoader(client *redis.Client) *ScriptLoader {
	return &ScriptLoader{
		client:      client,
		scriptCache: make(map[string]*redis.Script),
	}
}

// Load registers a script and returns a redis.Script instance
func (sl *ScriptLoader) Load(name, scriptSource string) *redis.Script {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if script, exists := sl.scriptCache[name]; exists {
		return script
	}

	script := redis.NewScript(scriptSource)
	sl.scriptCache[name] = script
	return script
}

// Run executes a script by name with EVALSHA (falls back to EVAL if not cached)
func (sl *ScriptLoader) Run(ctx context.Context, name string, keys []string, args ...interface{}) *redis.Cmd {
	script := sl.getScript(name)
	if script == nil {
		return redis.NewCmd(ctx, "ERR", "script not found: "+name)
	}
	return script.Run(ctx, sl.client, keys, args...)
}

// getScript retrieves a cached script
func (sl *ScriptLoader) getScript(name string) *redis.Script {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.scriptCache[name]
}

// GetSHA1 returns the SHA1 hash of a script
func GetSHA1(scriptSource string) string {
	h := sha1.New()
	h.Write([]byte(scriptSource))
	return hex.EncodeToString(h.Sum(nil))
}

// ScriptNames for easy reference
const (
	ScriptMoveToActive           = "moveToActive"
	ScriptMoveToCompleted        = "moveToCompleted"
	ScriptMoveToFailed           = "moveToFailed"
	ScriptRetryJob               = "retryJob"
	ScriptMoveStalledJobsToWait  = "moveStalledJobsToWait"
	ScriptExtendLock             = "extendLock"
	ScriptUpdateProgress         = "updateProgress"
	ScriptAddLog                 = "addLog"
)

// LoadAll loads all BullMQ scripts into the loader
func (sl *ScriptLoader) LoadAll() {
	sl.Load(ScriptMoveToActive, MoveToActive)
	sl.Load(ScriptMoveToCompleted, MoveToCompleted)
	sl.Load(ScriptMoveToFailed, MoveToFailed)
	sl.Load(ScriptRetryJob, RetryJob)
	sl.Load(ScriptMoveStalledJobsToWait, MoveStalledJobsToWait)
	sl.Load(ScriptExtendLock, ExtendLock)
	sl.Load(ScriptUpdateProgress, UpdateProgress)
	sl.Load(ScriptAddLog, AddLog)
}
