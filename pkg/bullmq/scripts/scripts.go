package scripts

// Lua scripts for atomic Redis operations
// Extracted from BullMQ v5.62.0 (commit 6a31e0aeab1311d7d089811ede7e11a98b6dd408)

const (
	// MoveToActive moves a job from wait/prioritized to active with lock acquisition
	MoveToActive = `
-- moveToActive.lua stub
-- TODO: Extract from BullMQ v5.62.0
return {jobId, lockToken}
`

	// MoveToCompleted moves a job from active to completed
	MoveToCompleted = `
-- moveToCompleted.lua stub
-- TODO: Extract from BullMQ v5.62.0
return 1
`

	// MoveToFailed moves a job from active to failed
	MoveToFailed = `
-- moveToFailed.lua stub
-- TODO: Extract from BullMQ v5.62.0
return 1
`

	// RetryJob retries a failed job with backoff
	RetryJob = `
-- retryJob.lua stub
-- TODO: Extract from BullMQ v5.62.0
return 1
`

	// MoveStalledJobsToWait detects and requeues stalled jobs
	MoveStalledJobsToWait = `
-- moveStalledJobsToWait.lua stub
-- TODO: Extract from BullMQ v5.62.0
return {}
`

	// ExtendLock extends a job's lock TTL (heartbeat)
	ExtendLock = `
-- extendLock.lua stub
-- TODO: Extract from BullMQ v5.62.0
return 1
`

	// UpdateProgress updates job progress
	UpdateProgress = `
-- updateProgress.lua stub
-- TODO: Extract from BullMQ v5.62.0
return 1
`

	// AddLog appends a log entry to job logs
	AddLog = `
-- addLog.lua stub
-- TODO: Extract from BullMQ v5.62.0
return 1
`
)
