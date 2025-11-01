package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq/scripts"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

// TestLuaScripts_MoveToActive tests the moveToActive Lua script with real Redis
func TestLuaScripts_MoveToActive(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15, // Use DB 15 for testing
	})
	defer rdb.Close()

	// Clean up before test
	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-queue"
	jobID := "job-1"
	keyPrefix := "bull:" + queueName + ":"

	// Setup: Add a job to the wait queue
	waitKey := keyPrefix + "wait"
	require.NoError(t, rdb.LPush(ctx, waitKey, jobID).Err())

	// Setup: Create job hash
	jobKey := keyPrefix + jobID
	jobData := map[string]interface{}{
		"id":       jobID,
		"name":     "test-job",
		"data":     `{"foo":"bar"}`,
		"opts":     "{}",
		"priority": 0,
		"delay":    0,
	}
	require.NoError(t, rdb.HSet(ctx, jobKey, jobData).Err())

	// Prepare script arguments
	timestamp := time.Now().UnixMilli()
	lockToken := "test-token-123"
	lockDuration := 30000 // 30 seconds

	optsMap := map[string]interface{}{
		"token":        lockToken,
		"lockDuration": lockDuration,
		"limiter":      nil,
		"name":         "test-worker",
	}
	optsPacked, err := msgpack.Marshal(optsMap)
	require.NoError(t, err)

	// Execute moveToActive script
	keys := []string{
		keyPrefix + "wait",       // KEYS[1]
		keyPrefix + "active",     // KEYS[2]
		keyPrefix + "prioritized", // KEYS[3]
		keyPrefix + "events",     // KEYS[4]
		keyPrefix + "stalled",    // KEYS[5]
		keyPrefix + "limiter",    // KEYS[6]
		keyPrefix + "delayed",    // KEYS[7]
		keyPrefix + "paused",     // KEYS[8]
		keyPrefix + "meta",       // KEYS[9]
		keyPrefix + "pc",         // KEYS[10]
		keyPrefix + "marker",     // KEYS[11]
	}

	args := []interface{}{
		keyPrefix,       // ARGV[1] - key prefix
		timestamp,       // ARGV[2] - timestamp
		string(optsPacked), // ARGV[3] - opts
	}

	result := rdb.Eval(ctx, scripts.MoveToActive, keys, args...)
	require.NoError(t, result.Err(), "moveToActive script should execute successfully")

	resultData, err := result.Slice()
	require.NoError(t, err)
	require.Len(t, resultData, 4, "moveToActive should return 4 elements")

	// Verify job was moved to active list
	activeJobs, err := rdb.LRange(ctx, keyPrefix+"active", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, activeJobs, jobID, "Job should be in active list")

	// Verify job was removed from wait list
	waitJobs, err := rdb.LRange(ctx, waitKey, 0, -1).Result()
	require.NoError(t, err)
	assert.NotContains(t, waitJobs, jobID, "Job should be removed from wait list")

	// Verify lock was created
	lockKey := jobKey + ":lock"
	lockValue, err := rdb.Get(ctx, lockKey).Result()
	require.NoError(t, err)
	assert.Equal(t, lockToken, lockValue, "Lock should be set with correct token")

	// Verify lock TTL
	ttl, err := rdb.PTTL(ctx, lockKey).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl.Milliseconds(), int64(0), "Lock should have TTL")
	assert.LessOrEqual(t, ttl.Milliseconds(), int64(lockDuration), "Lock TTL should not exceed lockDuration")

	t.Logf("✅ moveToActive script works correctly!")
	t.Logf("   Job moved from wait → active")
	t.Logf("   Lock acquired with token: %s", lockToken)
	t.Logf("   Lock TTL: %dms", ttl.Milliseconds())
}

// TestLuaScripts_ExtendLock tests the extendLock Lua script
func TestLuaScripts_ExtendLock(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer rdb.Close()

	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-queue"
	jobID := "job-1"
	keyPrefix := "bull:" + queueName + ":"
	lockKey := keyPrefix + jobID + ":lock"
	stalledKey := keyPrefix + "stalled"
	lockToken := "test-token-456"

	// Setup: Create initial lock
	require.NoError(t, rdb.Set(ctx, lockKey, lockToken, 5*time.Second).Err())
	require.NoError(t, rdb.SAdd(ctx, stalledKey, jobID).Err())

	// Get initial TTL
	initialTTL, err := rdb.PTTL(ctx, lockKey).Result()
	require.NoError(t, err)

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Execute extendLock script
	keys := []string{lockKey, stalledKey}
	args := []interface{}{lockToken, 30000, jobID} // Extend to 30 seconds

	result := rdb.Eval(ctx, scripts.ExtendLock, keys, args...)
	require.NoError(t, result.Err())

	extendResult, err := result.Int()
	require.NoError(t, err)
	assert.Equal(t, 1, extendResult, "extendLock should return 1 on success")

	// Verify lock was extended
	newTTL, err := rdb.PTTL(ctx, lockKey).Result()
	require.NoError(t, err)
	assert.Greater(t, newTTL.Milliseconds(), initialTTL.Milliseconds(), "Lock TTL should be extended")

	// Verify job removed from stalled set
	isMember, err := rdb.SIsMember(ctx, stalledKey, jobID).Result()
	require.NoError(t, err)
	assert.False(t, isMember, "Job should be removed from stalled set")

	t.Logf("✅ extendLock script works correctly!")
	t.Logf("   Initial TTL: %dms → New TTL: %dms", initialTTL.Milliseconds(), newTTL.Milliseconds())
	t.Logf("   Job removed from stalled set")
}

// TestLuaScripts_MoveToFinished tests job completion
func TestLuaScripts_MoveToFinished(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer rdb.Close()

	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-queue"
	jobID := "job-1"
	keyPrefix := "bull:" + queueName + ":"
	jobKey := keyPrefix + jobID
	lockKey := jobKey + ":lock"
	lockToken := "test-token-789"

	// Setup: Add job to active list
	require.NoError(t, rdb.LPush(ctx, keyPrefix+"active", jobID).Err())

	// Setup: Create job hash
	jobData := map[string]interface{}{
		"id":       jobID,
		"name":     "test-job",
		"data":     `{"result":"success"}`,
		"opts":     `{}`,
		"priority": 0,
		"atm":      0, // attempts made
	}
	require.NoError(t, rdb.HSet(ctx, jobKey, jobData).Err())

	// Setup: Create lock
	require.NoError(t, rdb.Set(ctx, lockKey, lockToken, 30*time.Second).Err())

	// Prepare opts
	optsMap := map[string]interface{}{
		"token": lockToken,
		"keepJobs": map[string]interface{}{
			"count": 100,
			"age":   nil,
		},
		"attempts":       3,
		"maxMetricsSize": "",
	}
	optsPacked, err := msgpack.Marshal(optsMap)
	require.NoError(t, err)

	// Execute moveToFinished script
	keys := []string{
		keyPrefix + "wait",       // KEYS[1]
		keyPrefix + "active",     // KEYS[2]
		keyPrefix + "prioritized", // KEYS[3]
		keyPrefix + "events",     // KEYS[4]
		keyPrefix + "stalled",    // KEYS[5]
		keyPrefix + "limiter",    // KEYS[6]
		keyPrefix + "delayed",    // KEYS[7]
		keyPrefix + "paused",     // KEYS[8]
		keyPrefix + "meta",       // KEYS[9]
		keyPrefix + "pc",         // KEYS[10]
		keyPrefix + "completed",  // KEYS[11]
		jobKey,                   // KEYS[12]
		keyPrefix + "metrics",    // KEYS[13]
		keyPrefix + "marker",     // KEYS[14]
	}

	timestamp := time.Now().UnixMilli()
	returnValue := `{"status":"done"}`

	args := []interface{}{
		jobID,              // ARGV[1]
		timestamp,          // ARGV[2]
		"returnvalue",      // ARGV[3]
		returnValue,        // ARGV[4]
		"completed",        // ARGV[5]
		0,                  // ARGV[6] - fetch next? (0=no)
		keyPrefix,          // ARGV[7]
		string(optsPacked), // ARGV[8]
		"",                 // ARGV[9] - job fields to update
	}

	result := rdb.Eval(ctx, scripts.MoveToFinished, keys, args...)
	require.NoError(t, result.Err(), "moveToFinished should execute successfully")

	resultCode, err := result.Int()
	require.NoError(t, err)
	assert.Equal(t, 0, resultCode, "moveToFinished should return 0 on success")

	// Verify job moved to completed set
	completedJobs, err := rdb.ZRange(ctx, keyPrefix+"completed", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, completedJobs, jobID, "Job should be in completed set")

	// Verify job removed from active list
	activeJobs, err := rdb.LRange(ctx, keyPrefix+"active", 0, -1).Result()
	require.NoError(t, err)
	assert.NotContains(t, activeJobs, jobID, "Job should be removed from active list")

	// Verify lock was released
	lockExists, err := rdb.Exists(ctx, lockKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), lockExists, "Lock should be released")

	// Verify returnvalue was stored
	storedReturnValue, err := rdb.HGet(ctx, jobKey, "returnvalue").Result()
	require.NoError(t, err)
	assert.Equal(t, returnValue, storedReturnValue, "Return value should be stored")

	// Verify finishedOn timestamp
	finishedOn, err := rdb.HGet(ctx, jobKey, "finishedOn").Result()
	require.NoError(t, err)
	assert.NotEmpty(t, finishedOn, "finishedOn timestamp should be set")

	t.Logf("✅ moveToFinished script works correctly!")
	t.Logf("   Job moved from active → completed")
	t.Logf("   Lock released")
	t.Logf("   Return value: %s", returnValue)
}

// TestLuaScripts_CompleteJobLifecycle tests a complete job flow
func TestLuaScripts_CompleteJobLifecycle(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer rdb.Close()

	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "lifecycle-test"
	jobID := "job-lifecycle-1"
	keyPrefix := "bull:" + queueName + ":"
	lockToken := "lifecycle-token"

	t.Log("📝 Step 1: Add job to wait queue")
	require.NoError(t, rdb.LPush(ctx, keyPrefix+"wait", jobID).Err())
	jobData := map[string]interface{}{
		"id":       jobID,
		"name":     "lifecycle-job",
		"data":     `{"task":"process data"}`,
		"opts":     `{}`,
		"priority": 0,
		"delay":    0,
		"atm":      0,
	}
	require.NoError(t, rdb.HSet(ctx, keyPrefix+jobID, jobData).Err())
	t.Log("   ✓ Job in wait queue")

	t.Log("📝 Step 2: Move to active (worker picks up job)")
	optsMap := map[string]interface{}{
		"token":        lockToken,
		"lockDuration": 30000,
		"name":         "test-worker-1",
	}
	optsPacked, _ := msgpack.Marshal(optsMap)

	moveToActiveKeys := []string{
		keyPrefix + "wait", keyPrefix + "active", keyPrefix + "prioritized",
		keyPrefix + "events", keyPrefix + "stalled", keyPrefix + "limiter",
		keyPrefix + "delayed", keyPrefix + "paused", keyPrefix + "meta",
		keyPrefix + "pc", keyPrefix + "marker",
	}
	moveToActiveArgs := []interface{}{keyPrefix, time.Now().UnixMilli(), string(optsPacked)}

	result := rdb.Eval(ctx, scripts.MoveToActive, moveToActiveKeys, moveToActiveArgs...)
	require.NoError(t, result.Err())
	t.Log("   ✓ Job moved to active, lock acquired")

	t.Log("📝 Step 3: Extend lock (heartbeat)")
	time.Sleep(100 * time.Millisecond)
	extendKeys := []string{keyPrefix + jobID + ":lock", keyPrefix + "stalled"}
	extendArgs := []interface{}{lockToken, 30000, jobID}
	extendResult := rdb.Eval(ctx, scripts.ExtendLock, extendKeys, extendArgs...)
	require.NoError(t, extendResult.Err())
	t.Log("   ✓ Lock extended (heartbeat successful)")

	t.Log("📝 Step 4: Complete job")
	finishOptsMap := map[string]interface{}{
		"token": lockToken,
		"keepJobs": map[string]interface{}{
			"count": 100,
			"age":   nil,
		},
		"attempts":       3,
		"maxMetricsSize": "",
	}
	finishOptsPacked, _ := msgpack.Marshal(finishOptsMap)

	moveToFinishedKeys := []string{
		keyPrefix + "wait", keyPrefix + "active", keyPrefix + "prioritized",
		keyPrefix + "events", keyPrefix + "stalled", keyPrefix + "limiter",
		keyPrefix + "delayed", keyPrefix + "paused", keyPrefix + "meta",
		keyPrefix + "pc", keyPrefix + "completed", keyPrefix + jobID,
		keyPrefix + "metrics", keyPrefix + "marker",
	}
	moveToFinishedArgs := []interface{}{
		jobID, time.Now().UnixMilli(), "returnvalue",
		`{"status":"success","processedItems":42}`, "completed",
		0, keyPrefix, string(finishOptsPacked), "",
	}

	finishResult := rdb.Eval(ctx, scripts.MoveToFinished, moveToFinishedKeys, moveToFinishedArgs...)
	require.NoError(t, finishResult.Err())
	t.Log("   ✓ Job completed successfully")

	// Final verification
	completedJobs, _ := rdb.ZRange(ctx, keyPrefix+"completed", 0, -1).Result()
	assert.Contains(t, completedJobs, jobID)
	activeJobs, _ := rdb.LRange(ctx, keyPrefix+"active", 0, -1).Result()
	assert.NotContains(t, activeJobs, jobID)
	waitJobs, _ := rdb.LRange(ctx, keyPrefix+"wait", 0, -1).Result()
	assert.NotContains(t, waitJobs, jobID)

	t.Log("")
	t.Log("🎉 COMPLETE JOB LIFECYCLE TEST PASSED!")
	t.Log("   wait → active → (heartbeat) → completed")
	t.Log("   All Lua scripts working with Redis!")
}

// TestLuaScripts_UpdateProgress tests progress updates
func TestLuaScripts_UpdateProgress(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer rdb.Close()

	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-queue"
	jobID := "job-progress"
	keyPrefix := "bull:" + queueName + ":"
	jobKey := keyPrefix + jobID

	// Setup: Create job
	jobData := map[string]interface{}{
		"id":   jobID,
		"name": "progress-job",
	}
	require.NoError(t, rdb.HSet(ctx, jobKey, jobData).Err())

	// Setup: Set maxEvents in meta
	require.NoError(t, rdb.HSet(ctx, keyPrefix+"meta", "opts.maxLenEvents", 10000).Err())

	// Execute updateProgress script
	keys := []string{
		jobKey,              // KEYS[1]
		keyPrefix + "events", // KEYS[2]
		keyPrefix + "meta",   // KEYS[3]
	}
	args := []interface{}{
		jobID, // ARGV[1]
		50,    // ARGV[2] - progress (50%)
	}

	result := rdb.Eval(ctx, scripts.UpdateProgress, keys, args...)
	require.NoError(t, result.Err())

	resultCode, err := result.Int()
	require.NoError(t, err)
	assert.Equal(t, 0, resultCode, "updateProgress should return 0 on success")

	// Verify progress was updated
	progress, err := rdb.HGet(ctx, jobKey, "progress").Result()
	require.NoError(t, err)
	assert.Equal(t, "50", progress, "Progress should be set to 50")

	t.Logf("✅ updateProgress script works correctly!")
	t.Logf("   Progress updated to: %s%%", progress)
}

// TestLuaScripts_AddLog tests log appending
func TestLuaScripts_AddLog(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer rdb.Close()

	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "test-queue"
	jobID := "job-logs"
	keyPrefix := "bull:" + queueName + ":"
	jobKey := keyPrefix + jobID
	logsKey := jobKey + ":logs"

	// Setup: Create job
	require.NoError(t, rdb.HSet(ctx, jobKey, "id", jobID).Err())

	// Execute addLog script multiple times
	keys := []string{jobKey, logsKey}

	logMessages := []string{
		"Starting job processing",
		"Processing item 1/10",
		"Processing item 5/10",
		"Job completed successfully",
	}

	for _, msg := range logMessages {
		msgJSON, _ := json.Marshal(map[string]string{"message": msg, "timestamp": time.Now().Format(time.RFC3339)})
		args := []interface{}{
			jobID,          // ARGV[1]
			string(msgJSON), // ARGV[2]
			"1000",         // ARGV[3] - keep logs (max 1000)
		}

		result := rdb.Eval(ctx, scripts.AddLog, keys, args...)
		require.NoError(t, result.Err())
	}

	// Verify logs were added
	logs, err := rdb.LRange(ctx, logsKey, 0, -1).Result()
	require.NoError(t, err)
	assert.Len(t, logs, len(logMessages), "All log messages should be stored")

	t.Logf("✅ addLog script works correctly!")
	t.Logf("   %d log entries added", len(logs))
	for i, log := range logs {
		t.Logf("   [%d] %s", i+1, log)
	}
}

// TestLuaScripts_RetryJob tests the retry mechanism
func TestLuaScripts_RetryJob(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer rdb.Close()

	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "retry-queue"
	jobID := "job-retry-1"
	keyPrefix := "bull:" + queueName + ":"
	jobKey := keyPrefix + jobID
	lockKey := jobKey + ":lock"
	lockToken := "retry-token-123"

	// Setup: Add job to active list
	require.NoError(t, rdb.LPush(ctx, keyPrefix+"active", jobID).Err())

	// Setup: Create job hash with attemptsMade
	jobData := map[string]interface{}{
		"id":       jobID,
		"name":     "retry-job",
		"data":     `{"operation":"flaky"}`,
		"opts":     `{}`,
		"priority": 0,
		"atm":      1, // 1 attempt already made
	}
	require.NoError(t, rdb.HSet(ctx, jobKey, jobData).Err())

	// Setup: Create lock
	require.NoError(t, rdb.Set(ctx, lockKey, lockToken, 30*time.Second).Err())

	// Setup: Add to stalled set
	require.NoError(t, rdb.SAdd(ctx, keyPrefix+"stalled", jobID).Err())

	// Prepare job fields to update (failedReason, finishedOn)
	fieldsToUpdate := []interface{}{
		"failedReason", "Transient network error",
		"finishedOn", time.Now().UnixMilli(),
	}
	fieldsPacked, err := msgpack.Marshal(fieldsToUpdate)
	require.NoError(t, err)

	// Execute retryJob script
	keys := []string{
		keyPrefix + "active",      // KEYS[1]
		keyPrefix + "wait",        // KEYS[2]
		keyPrefix + "paused",      // KEYS[3]
		jobKey,                    // KEYS[4]
		keyPrefix + "meta",        // KEYS[5]
		keyPrefix + "events",      // KEYS[6]
		keyPrefix + "delayed",     // KEYS[7]
		keyPrefix + "prioritized", // KEYS[8]
		keyPrefix + "pc",          // KEYS[9]
		keyPrefix + "marker",      // KEYS[10]
		keyPrefix + "stalled",     // KEYS[11]
	}

	args := []interface{}{
		keyPrefix,          // ARGV[1] - key prefix
		time.Now().UnixMilli(), // ARGV[2] - timestamp
		"RPUSH",            // ARGV[3] - push command (RPUSH for FIFO)
		jobID,              // ARGV[4]
		lockToken,          // ARGV[5]
		string(fieldsPacked), // ARGV[6] - job fields to update
	}

	result := rdb.Eval(ctx, scripts.RetryJob, keys, args...)
	require.NoError(t, result.Err(), "retryJob script should execute successfully")

	resultCode, err := result.Int()
	require.NoError(t, err)
	assert.Equal(t, 0, resultCode, "retryJob should return 0 on success")

	// Verify job was moved back to wait queue
	waitJobs, err := rdb.LRange(ctx, keyPrefix+"wait", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, waitJobs, jobID, "Job should be in wait queue")

	// Verify job was removed from active list
	activeJobs, err := rdb.LRange(ctx, keyPrefix+"active", 0, -1).Result()
	require.NoError(t, err)
	assert.NotContains(t, activeJobs, jobID, "Job should be removed from active list")

	// Verify lock was released
	lockExists, err := rdb.Exists(ctx, lockKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), lockExists, "Lock should be released")

	// Verify attemptsMade was incremented
	attemptsMade, err := rdb.HGet(ctx, jobKey, "atm").Result()
	require.NoError(t, err)
	assert.Equal(t, "2", attemptsMade, "attemptsMade should be incremented to 2")

	// Verify failedReason was stored
	failedReason, err := rdb.HGet(ctx, jobKey, "failedReason").Result()
	require.NoError(t, err)
	assert.Equal(t, "Transient network error", failedReason, "Failed reason should be stored")

	t.Logf("✅ retryJob script works correctly!")
	t.Logf("   Job moved from active → wait (for retry)")
	t.Logf("   Lock released")
	t.Logf("   Attempts: 1 → 2")
	t.Logf("   Ready for retry!")
}

// TestLuaScripts_MoveStalledJobsToWait tests stalled job detection
func TestLuaScripts_MoveStalledJobsToWait(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer rdb.Close()

	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "stalled-queue"
	keyPrefix := "bull:" + queueName + ":"

	// Setup: Create 3 jobs in active state
	job1 := "job-stalled-1"
	job2 := "job-stalled-2"
	job3 := "job-healthy-3"

	// Jobs 1 and 2: stalled (no lock exists)
	require.NoError(t, rdb.LPush(ctx, keyPrefix+"active", job1, job2, job3).Err())

	// Create job hashes
	for _, jobID := range []string{job1, job2, job3} {
		jobData := map[string]interface{}{
			"id":   jobID,
			"name": "stalled-test",
			"data": `{}`,
			"opts": `{}`,
			"stc":  0, // stalled count
			"atm":  1, // attempts made
		}
		require.NoError(t, rdb.HSet(ctx, keyPrefix+jobID, jobData).Err())
	}

	// Job 3 has a valid lock (not stalled)
	require.NoError(t, rdb.Set(ctx, keyPrefix+job3+":lock", "valid-token", 30*time.Second).Err())

	// Mark all jobs as potentially stalled
	require.NoError(t, rdb.SAdd(ctx, keyPrefix+"stalled", job1, job2, job3).Err())

	// Execute moveStalledJobsToWait script
	keys := []string{
		keyPrefix + "stalled",       // KEYS[1]
		keyPrefix + "wait",          // KEYS[2]
		keyPrefix + "active",        // KEYS[3]
		keyPrefix + "stalled-check", // KEYS[4]
		keyPrefix + "meta",          // KEYS[5]
		keyPrefix + "paused",        // KEYS[6]
		keyPrefix + "marker",        // KEYS[7]
		keyPrefix + "events",        // KEYS[8]
	}

	args := []interface{}{
		50,                 // ARGV[1] - max stalled job count before failing
		keyPrefix,          // ARGV[2] - queue key prefix
		time.Now().UnixMilli(), // ARGV[3] - timestamp
		30000,              // ARGV[4] - max check time (30s)
	}

	result := rdb.Eval(ctx, scripts.MoveStalledJobsToWait, keys, args...)
	require.NoError(t, result.Err(), "moveStalledJobsToWait should execute successfully")

	stalledJobs, err := result.Slice()
	require.NoError(t, err)
	t.Logf("Stalled jobs detected: %v", stalledJobs)

	// Verify stalled jobs (job1, job2) were moved to wait
	waitJobs, err := rdb.LRange(ctx, keyPrefix+"wait", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, waitJobs, job1, "Job1 should be in wait queue")
	assert.Contains(t, waitJobs, job2, "Job2 should be in wait queue")
	assert.NotContains(t, waitJobs, job3, "Job3 (healthy) should NOT be in wait queue")

	// Verify job3 is still in active (has valid lock)
	activeJobs, err := rdb.LRange(ctx, keyPrefix+"active", 0, -1).Result()
	require.NoError(t, err)
	assert.Contains(t, activeJobs, job3, "Job3 should still be in active list")

	// Verify stalled count was incremented
	stc1, err := rdb.HGet(ctx, keyPrefix+job1, "stc").Result()
	require.NoError(t, err)
	assert.Equal(t, "1", stc1, "Stalled count should be incremented")

	stc2, err := rdb.HGet(ctx, keyPrefix+job2, "stc").Result()
	require.NoError(t, err)
	assert.Equal(t, "1", stc2, "Stalled count should be incremented")

	t.Logf("✅ moveStalledJobsToWait script works correctly!")
	t.Logf("   Detected %d stalled jobs (job1, job2)", len(stalledJobs))
	t.Logf("   Moved to wait queue for retry")
	t.Logf("   Job3 (healthy with lock) remained in active")
	t.Logf("   Stalled counts incremented")
}

// TestLuaScripts_CompleteRetryFlow tests failure → retry → success
func TestLuaScripts_CompleteRetryFlow(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer rdb.Close()

	require.NoError(t, rdb.FlushDB(ctx).Err())

	queueName := "retry-flow"
	jobID := "job-retry-flow"
	keyPrefix := "bull:" + queueName + ":"
	lockToken := "flow-token"

	t.Log("🔄 RETRY FLOW TEST: Failure → Retry → Success")
	t.Log("")

	// ==================== ATTEMPT 1: INITIAL FAILURE ====================
	t.Log("📝 Attempt 1: Job fails with transient error")

	// Add job to wait queue
	require.NoError(t, rdb.LPush(ctx, keyPrefix+"wait", jobID).Err())
	jobData := map[string]interface{}{
		"id":       jobID,
		"name":     "flaky-job",
		"data":     `{"url":"https://api.example.com/data"}`,
		"opts":     `{}`,
		"priority": 0,
		"atm":      0,
	}
	require.NoError(t, rdb.HSet(ctx, keyPrefix+jobID, jobData).Err())

	// Move to active
	optsMap := map[string]interface{}{
		"token":        lockToken,
		"lockDuration": 30000,
		"name":         "worker-1",
	}
	optsPacked, _ := msgpack.Marshal(optsMap)

	moveToActiveKeys := []string{
		keyPrefix + "wait", keyPrefix + "active", keyPrefix + "prioritized",
		keyPrefix + "events", keyPrefix + "stalled", keyPrefix + "limiter",
		keyPrefix + "delayed", keyPrefix + "paused", keyPrefix + "meta",
		keyPrefix + "pc", keyPrefix + "marker",
	}
	rdb.Eval(ctx, scripts.MoveToActive, moveToActiveKeys, []interface{}{
		keyPrefix, time.Now().UnixMilli(), string(optsPacked),
	})

	t.Log("   ✓ Job picked up by worker")

	// Simulate failure → retry
	fieldsToUpdate := []interface{}{
		"failedReason", "Network timeout (transient)",
	}
	fieldsPacked, _ := msgpack.Marshal(fieldsToUpdate)

	retryKeys := []string{
		keyPrefix + "active", keyPrefix + "wait", keyPrefix + "paused",
		keyPrefix + jobID, keyPrefix + "meta", keyPrefix + "events",
		keyPrefix + "delayed", keyPrefix + "prioritized", keyPrefix + "pc",
		keyPrefix + "marker", keyPrefix + "stalled",
	}
	rdb.Eval(ctx, scripts.RetryJob, retryKeys, []interface{}{
		keyPrefix, time.Now().UnixMilli(), "RPUSH", jobID, lockToken, string(fieldsPacked),
	})

	t.Log("   ✓ Job failed → moved back to wait queue (retry #1)")

	// Verify attemptsMade incremented
	atm1, _ := rdb.HGet(ctx, keyPrefix+jobID, "atm").Result()
	assert.Equal(t, "1", atm1, "First attempt recorded")

	// ==================== ATTEMPT 2: STILL FAILING ====================
	t.Log("")
	t.Log("📝 Attempt 2: Job fails again")

	// Pick up again
	lockToken2 := "flow-token-2"
	optsMap2 := map[string]interface{}{
		"token":        lockToken2,
		"lockDuration": 30000,
		"name":         "worker-1",
	}
	optsPacked2, _ := msgpack.Marshal(optsMap2)

	rdb.Eval(ctx, scripts.MoveToActive, moveToActiveKeys, []interface{}{
		keyPrefix, time.Now().UnixMilli(), string(optsPacked2),
	})

	// Fail again
	fieldsToUpdate2 := []interface{}{
		"failedReason", "Connection refused (transient)",
	}
	fieldsPacked2, _ := msgpack.Marshal(fieldsToUpdate2)

	rdb.Eval(ctx, scripts.RetryJob, retryKeys, []interface{}{
		keyPrefix, time.Now().UnixMilli(), "RPUSH", jobID, lockToken2, string(fieldsPacked2),
	})

	t.Log("   ✓ Job failed again → retry #2")

	atm2, _ := rdb.HGet(ctx, keyPrefix+jobID, "atm").Result()
	assert.Equal(t, "2", atm2, "Second attempt recorded")

	// ==================== ATTEMPT 3: SUCCESS ====================
	t.Log("")
	t.Log("📝 Attempt 3: Job succeeds!")

	// Pick up one more time
	lockToken3 := "flow-token-3"
	optsMap3 := map[string]interface{}{
		"token":        lockToken3,
		"lockDuration": 30000,
		"name":         "worker-1",
	}
	optsPacked3, _ := msgpack.Marshal(optsMap3)

	rdb.Eval(ctx, scripts.MoveToActive, moveToActiveKeys, []interface{}{
		keyPrefix, time.Now().UnixMilli(), string(optsPacked3),
	})

	// This time it succeeds!
	finishOptsMap := map[string]interface{}{
		"token": lockToken3,
		"keepJobs": map[string]interface{}{
			"count": 100,
			"age":   nil,
		},
		"attempts":       5,
		"maxMetricsSize": "",
	}
	finishOptsPacked, _ := msgpack.Marshal(finishOptsMap)

	moveToFinishedKeys := []string{
		keyPrefix + "wait", keyPrefix + "active", keyPrefix + "prioritized",
		keyPrefix + "events", keyPrefix + "stalled", keyPrefix + "limiter",
		keyPrefix + "delayed", keyPrefix + "paused", keyPrefix + "meta",
		keyPrefix + "pc", keyPrefix + "completed", keyPrefix + jobID,
		keyPrefix + "metrics", keyPrefix + "marker",
	}

	rdb.Eval(ctx, scripts.MoveToFinished, moveToFinishedKeys, []interface{}{
		jobID, time.Now().UnixMilli(), "returnvalue",
		`{"status":"success","data":"fetched successfully after 3 attempts"}`,
		"completed", 0, keyPrefix, string(finishOptsPacked), "",
	})

	t.Log("   ✓ Job completed successfully!")

	// Final verification
	completedJobs, _ := rdb.ZRange(ctx, keyPrefix+"completed", 0, -1).Result()
	assert.Contains(t, completedJobs, jobID, "Job should be in completed set")

	atm3, _ := rdb.HGet(ctx, keyPrefix+jobID, "atm").Result()
	assert.Equal(t, "3", atm3, "Final attempt count: 3")

	returnValue, _ := rdb.HGet(ctx, keyPrefix+jobID, "returnvalue").Result()
	assert.Contains(t, returnValue, "success", "Success result stored")

	t.Log("")
	t.Log("🎉 COMPLETE RETRY FLOW SUCCESSFUL!")
	t.Log("   Attempt 1: Failed (Network timeout)")
	t.Log("   Attempt 2: Failed (Connection refused)")
	t.Log("   Attempt 3: ✅ SUCCESS")
	t.Log("")
	t.Log("   Final state:")
	t.Logf("   - Attempts made: %s", atm3)
	t.Logf("   - Status: Completed")
	t.Logf("   - Result: %s", returnValue)
}
