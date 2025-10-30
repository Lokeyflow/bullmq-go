# BullMQ Protocol Contracts

**Version**: BullMQ v5.62.0 (commit `6a31e0aeab1311d7d089811ede7e11a98b6dd408`)
**Date**: 2025-10-30
**Purpose**: Comprehensive Redis protocol specifications for BullMQ Go client library

---

## Overview

This directory contains the complete Redis protocol specifications for implementing a BullMQ-compatible Go client library. These contracts define the exact data structures, key patterns, Lua scripts, and event formats used by BullMQ.

---

## Files

### 1. redis-keys.md
**Purpose**: Redis key patterns and hash tag usage

**Contents**:
- All Redis key patterns (`bull:{queue}:wait`, `bull:{queue}:active`, etc.)
- Hash tag explanation for Redis Cluster compatibility
- Key lifecycle examples
- Expiration policies
- Redis Cluster slot calculation
- Testing strategies for hash tag compliance

**Key Insights**:
- All keys MUST use `bull:{queue-name}:*` format with hash tags
- Hash tags ensure all queue keys map to same Redis Cluster slot
- Prevents CROSSSLOT errors in multi-key Lua scripts

---

### 2. job-schema.json
**Purpose**: JSON Schema for job data structure

**Contents**:
- Complete job hash field definitions
- Field types, validation rules, and examples
- JobOptions definition (attempts, backoff, priority, etc.)
- Required vs optional fields
- Data type conventions (all strings in Redis hash)

**Key Insights**:
- All numeric fields stored as STRINGS in Redis
- JSON fields (data, opts, returnvalue) stored as escaped JSON strings
- Maximum payload size: 10MB (data + opts combined)
- `attemptsMade` starts at 0, increments on each retry

---

### 3. event-schema.json
**Purpose**: JSON Schema for event stream entries

**Contents**:
- Event type enumeration (waiting, active, progress, completed, failed, etc.)
- Event-specific field requirements
- Redis stream entry format
- Event lifecycle examples

**Key Insights**:
- Events emitted to `bull:{queue}:events` Redis stream
- Stream retention: MAXLEN ~10000 (approximate trim)
- All field values are STRINGS per Redis stream semantics
- Event emission is best-effort (not transactional)

---

### 4. lua-scripts.md
**Purpose**: Lua script signatures and behavior documentation

**Contents**:
- All 8 core Lua scripts with detailed signatures
- Script execution model and error handling
- Performance targets and optimization tips
- Version compatibility and CI validation strategy

**Scripts Documented**:
1. **moveToActive.lua** - Atomically move job to active and acquire lock
2. **moveToCompleted.lua** - Mark job as completed, release lock
3. **moveToFailed.lua** - Mark job as failed, schedule retry or move to DLQ
4. **retryJob.lua** - Schedule job for retry with exponential backoff
5. **moveStalledJobsToWait.lua** - Detect and requeue jobs with expired locks
6. **extendLock.lua** - Extend job lock TTL (heartbeat)
7. **updateProgress.lua** - Update job progress percentage
8. **addLog.lua** - Append log entry to job logs list

**Key Insights**:
- Lua scripts provide atomicity for multi-key operations
- Scripts cached by SHA1 for performance (EVALSHA vs EVAL)
- Lock token verification prevents lock hijacking
- Scripts pinned to BullMQ v5.62.0 commit for reproducibility

---

## Usage

### For Implementation
1. Read `redis-keys.md` first to understand key structure
2. Read `job-schema.json` to understand job data structure
3. Read `event-schema.json` to understand event format
4. Read `lua-scripts.md` to understand atomic operations

### For Testing
- Use JSON schemas for validation (job payloads, events)
- Use `redis-keys.md` test cases to verify hash tag compliance
- Use `lua-scripts.md` examples to validate script behavior

### For Documentation
- Reference schemas in API documentation
- Link to contracts in CLAUDE.md and README.md
- Use examples from schemas in code comments

---

## Version Compatibility

**Current Version**: BullMQ v5.62.0
**Commit SHA**: `6a31e0aeab1311d7d089811ede7e11a98b6dd408`
**Release Date**: 2025-10-28

**Why Pinned?**
- Exact commit pinning prevents protocol drift
- Ensures reproducible builds
- CI validates scripts match upstream on every build

**Migration Path**:
- When BullMQ protocol changes, update commit SHA
- Re-extract scripts, update schemas
- Run full compatibility test suite (Node.js ↔ Go)
- Update documentation with breaking changes

---

## Cross-Language Compatibility

These contracts are designed to ensure full interoperability with Node.js BullMQ:

1. **Node.js producer → Go worker**: ✅ Jobs processed correctly
2. **Go producer → Node.js worker**: ✅ Jobs processed correctly
3. **Shadow test**: ✅ Node.js + Go workers process same queue concurrently
4. **Redis state format**: ✅ Matches Node.js BullMQ exactly
5. **Event stream format**: ✅ Matches Node.js BullMQ exactly

---

## Testing Strategy

### Unit Tests
- Key builder (hash tag validation)
- Job schema validation (JSON Schema)
- Event schema validation (JSON Schema)

### Integration Tests
- Redis operations with actual Redis instance
- Lua script execution and return values
- Multi-key operations (hash tag compliance)

### Compatibility Tests
- Node.js BullMQ → Go worker (consume jobs)
- Go producer → Node.js worker (consume jobs)
- Redis state inspection (key format matches)

### Redis Cluster Tests
- 3-node cluster with testcontainers
- Validate no CROSSSLOT errors
- Validate all queue keys in same slot

---

## References

- [BullMQ Documentation](https://docs.bullmq.io/)
- [BullMQ v5.62.0 Source](https://github.com/taskforcesh/bullmq/tree/6a31e0aeab1311d7d089811ede7e11a98b6dd408)
- [Redis Lua Scripting](https://redis.io/docs/manual/programmability/eval-intro/)
- [Redis Cluster Hash Tags](https://redis.io/docs/reference/cluster-spec/#hash-tags)
- [JSON Schema Specification](https://json-schema.org/)

---

**Maintained By**: BullMQ Go Client Library Team
**Last Updated**: 2025-10-30
