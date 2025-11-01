# 🚨 CRITICAL BUG ANALYSIS: Cross-Language Key Incompatibility

## Executive Summary

**Severity**: CRITICAL (P0)
**Impact**: Cross-language compatibility broken on single-instance Redis
**Status**: Discovered post-release (v0.1.0)
**Timeline**: Fix required before production adoption

---

## 1. Problem Statement

### Current Behavior

**Go (bullmq-go v0.1.0)**: ALWAYS uses hash tags
```
bull:{video-generation}:wait
bull:{video-generation}:active
bull:{video-generation}:1
```

**Node.js BullMQ (default)**: NO hash tags on single-instance Redis
```
bull:video-generation:wait
bull:video-generation:active
bull:video-generation:1
```

**Node.js BullMQ (cluster mode)**: Hash tags via prefix configuration
```javascript
new Queue('video-generation', {
  prefix: '{video-generation}'  // Custom prefix for cluster
})
// Results in: {video-generation}:video-generation:wait
```

### The Incompatibility Matrix

| Setup | Node.js Producer | Go Worker | Result |
|-------|------------------|-----------|---------|
| Single-instance Redis | `bull:myqueue:wait` | Looking at `bull:{myqueue}:wait` | ❌ FAIL |
| Cluster (Node unconfigured) | `bull:myqueue:wait` | Looking at `bull:{myqueue}:wait` | ❌ FAIL |
| Cluster (Node configured) | `bull:{myqueue}:wait` | Looking at `bull:{myqueue}:wait` | ✅ PASS |
| Go-only (any mode) | `bull:{myqueue}:wait` | `bull:{myqueue}:wait` | ✅ PASS |

### Why This Matters

1. **Silent Failures**: Jobs created by Node.js are invisible to Go workers
2. **Development vs Production**: Works in cluster, fails in dev (single-instance)
3. **Cross-Language Promise**: Stated goal of the library is BullMQ protocol compatibility
4. **Common Use Case**: Mixed Node.js/Go deployments are the PRIMARY use case

---

## 2. Root Cause Analysis

### Why Hash Tags Exist

Redis Cluster distributes keys across 16,384 slots using CRC16 hashing:
- `bull:myqueue:wait` → hashes entire string → slot 1234
- `bull:myqueue:active` → hashes entire string → slot 5678
- **Problem**: Multi-key Lua scripts fail with CROSSSLOT error

Hash tags `{...}` tell Redis to only hash the content inside braces:
- `bull:{myqueue}:wait` → hashes only `myqueue` → slot 2331
- `bull:{myqueue}:active` → hashes only `myqueue` → slot 2331
- **Solution**: All keys in same slot, Lua scripts work

### Why Single-Instance Doesn't Need Them

Single-instance Redis:
- No clustering, all keys on one node
- Hash tags are **literal characters** (not special syntax)
- `bull:myqueue:wait` and `bull:{myqueue}:wait` are **different keys**

### Design Mistake

The Go library was designed "cluster-first":
- Assumed hash tags are always needed
- Prioritized cluster compatibility over single-instance compatibility
- Didn't consider cross-language use case on single-instance Redis

---

## 3. Impact Assessment

### Who's Affected

**High Impact**:
- ❌ Mixed Node.js/Go deployments on single-instance Redis (MOST COMMON)
- ❌ Development environments (usually single-instance)
- ❌ Migration from Node.js to Go (gradual adoption)

**Medium Impact**:
- ⚠️ Mixed deployments on unconfigured Redis Cluster
- ⚠️ Cross-language monitoring/debugging tools

**Low Impact**:
- ✅ Go-only deployments (works fine)
- ✅ Properly configured cluster deployments

### Risk Level

**If NOT Fixed**:
- **HIGH**: Adoption failure (primary use case broken)
- **HIGH**: Silent data loss (jobs disappear)
- **HIGH**: Debugging nightmare (keys don't match expectations)
- **MEDIUM**: Reputation damage (incompatible with stated goal)

**If Fixed**:
- **LOW**: Breaking change (v0.1.0 released <2 hours ago)
- **LOW**: No known production users yet
- **HIGH**: Correctness and compatibility achieved

---

## 4. Solution Design

### Design Principles

1. **Match Node.js Default**: No hash tags on single-instance Redis
2. **Auto-detect Cluster**: Add hash tags automatically when needed
3. **Explicit Override**: Allow forcing hash tags for special cases
4. **Zero Config**: Works correctly out-of-the-box
5. **Backwards Compatible**: Provide migration path (if needed)

### Proposed Implementation

#### 4.1 Auto-Detection Strategy

```go
type KeyBuilder struct {
    queueName   string
    useHashTags bool  // Auto-detected from Redis client type
}

func NewKeyBuilder(queueName string, client interface{}) *KeyBuilder {
    // Auto-detect cluster mode
    useHashTags := IsRedisCluster(client)

    return &KeyBuilder{
        queueName:   queueName,
        useHashTags: useHashTags,
    }
}

func (kb *KeyBuilder) Wait() string {
    if kb.useHashTags {
        return fmt.Sprintf("bull:{%s}:wait", kb.queueName)
    }
    return fmt.Sprintf("bull:%s:wait", kb.queueName)
}
```

**Detection Logic** (already exists):
```go
// IsRedisCluster checks if the Redis client is connected to a cluster
func IsRedisCluster(client interface{}) bool {
    _, isCluster := client.(*redis.ClusterClient)
    return isCluster
}
```

#### 4.2 Configuration Override

```go
type QueueOptions struct {
    // Existing options...

    // ForceHashTags forces hash tag usage even on single-instance Redis
    // Useful for testing cluster behavior or migrating existing deployments
    ForceHashTags bool  // Default: false (auto-detect)
}

type WorkerOptions struct {
    // Existing options...

    ForceHashTags bool  // Match queue configuration
}
```

#### 4.3 API Changes Required

**Current (BROKEN)**:
```go
// Queue
queue := bullmq.NewQueue("myqueue", redisClient)
// Worker
worker := bullmq.NewWorker("myqueue", redisClient, opts)

// KeyBuilder (internal)
kb := bullmq.NewKeyBuilder("myqueue")  // No client passed!
```

**Fixed (COMPATIBLE)**:
```go
// Queue - pass client to KeyBuilder
queue := bullmq.NewQueue("myqueue", redisClient)
// Internally: kb := NewKeyBuilder("myqueue", redisClient)

// Worker - pass client to KeyBuilder
worker := bullmq.NewWorker("myqueue", redisClient, opts)
// Internally: kb := NewKeyBuilder("myqueue", redisClient)

// Manual override
queue := bullmq.NewQueue("myqueue", redisClient, bullmq.QueueOptions{
    ForceHashTags: true,  // Use hash tags even on single-instance
})
```

---

## 5. Implementation Plan

### Phase 1: Core Fix (30 mins)

**Task 1.1**: Update KeyBuilder
- [ ] Add `useHashTags bool` field
- [ ] Update constructor to accept client and auto-detect
- [ ] Add conditional logic to all key methods (11 methods)
- [ ] File: `pkg/bullmq/keys.go`

**Task 1.2**: Update Queue
- [ ] Pass client to KeyBuilder constructor
- [ ] Add `ForceHashTags` to `QueueOptions`
- [ ] File: `pkg/bullmq/queue.go` and `pkg/bullmq/queue_impl.go`

**Task 1.3**: Update Worker
- [ ] Pass client to KeyBuilder constructor
- [ ] Add `ForceHashTags` to `WorkerOptions`
- [ ] File: `pkg/bullmq/worker.go` and `pkg/bullmq/worker_impl.go`

**Task 1.4**: Update Helper Functions
- [ ] Any other places KeyBuilder is instantiated
- [ ] Grep for `NewKeyBuilder` calls

### Phase 2: Testing (25 mins)

**Task 2.1**: Unit Tests
- [ ] Test KeyBuilder with `redis.Client` (single-instance) → no hash tags
- [ ] Test KeyBuilder with `redis.ClusterClient` → hash tags
- [ ] Test `ForceHashTags` override on single-instance
- [ ] File: `tests/unit/keys_test.go`

**Task 2.2**: Integration Tests
- [ ] Test Queue operations on single-instance (verify key format)
- [ ] Test Worker operations on single-instance (verify key format)
- [ ] Test cluster mode still works (existing tests should pass)
- [ ] File: `tests/integration/keys_format_test.go` (NEW)

**Task 2.3**: Cross-Language Compatibility Tests
- [ ] Create test that mimics Node.js key format
- [ ] Verify Go can read jobs created with Node.js key format
- [ ] Document testing procedure for actual Node.js integration
- [ ] File: `tests/integration/nodejs_compat_test.go` (NEW)

### Phase 3: Documentation (20 mins)

**Task 3.1**: Update CLAUDE.md
- [ ] Explain auto-detection behavior
- [ ] Document ForceHashTags option
- [ ] Add cross-language compatibility section
- [ ] Show example with Node.js interop

**Task 3.2**: Create Migration Guide
- [ ] Document Redis RENAME commands for v0.1.0 → v0.1.1
- [ ] Provide script to migrate keys
- [ ] File: `MIGRATION.md` (NEW)

**Task 3.3**: Update README
- [ ] Add cross-language compatibility section
- [ ] Highlight auto-detection feature
- [ ] File: `README.md`

### Phase 4: Release (15 mins)

**Task 4.1**: Version Strategy
- [ ] Keep v0.1.0 tag (already public, preserve history)
- [ ] Create new commit with fix
- [ ] Tag as v0.1.1
- [ ] Update PR #1 description to include v0.1.1 notes

**Task 4.2**: Update PR
- [ ] Add "HOTFIX" section to PR description
- [ ] Explain the bug and fix
- [ ] Update checklist

**Task 4.3**: Release Notes
- [ ] Create v0.1.1 release notes
- [ ] Highlight critical bug fix
- [ ] Provide migration instructions

---

## 6. Testing Strategy

### 6.1 Automated Tests

**Unit Tests** (`tests/unit/keys_test.go`):
```go
func TestKeyBuilder_SingleInstance_NoHashTags(t *testing.T) {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    kb := NewKeyBuilder("myqueue", client)

    assert.Equal(t, "bull:myqueue:wait", kb.Wait())
    assert.Equal(t, "bull:myqueue:active", kb.Active())
    assert.Equal(t, "bull:myqueue:1", kb.Job("1"))
}

func TestKeyBuilder_Cluster_WithHashTags(t *testing.T) {
    client := redis.NewClusterClient(&redis.ClusterOptions{
        Addrs: []string{"localhost:7001"},
    })
    kb := NewKeyBuilder("myqueue", client)

    assert.Equal(t, "bull:{myqueue}:wait", kb.Wait())
    assert.Equal(t, "bull:{myqueue}:active", kb.Active())
    assert.Equal(t, "bull:{myqueue}:1", kb.Job("1"))
}

func TestKeyBuilder_ForceHashTags(t *testing.T) {
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    kb := NewKeyBuilder("myqueue", client, true) // Force hash tags

    assert.Equal(t, "bull:{myqueue}:wait", kb.Wait())
}
```

**Integration Tests** (`tests/integration/nodejs_compat_test.go`):
```go
func TestNodeJSCompatibility_SingleInstance(t *testing.T) {
    ctx := context.Background()
    client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

    // Simulate Node.js creating a job (no hash tags)
    nodeJSKey := "bull:test-queue:1"
    client.HSet(ctx, nodeJSKey, "name", "test-job", "data", `{"foo":"bar"}`)
    client.LPush(ctx, "bull:test-queue:wait", "1")

    // Go worker should find it
    queue := NewQueue("test-queue", client)
    worker := NewWorker("test-queue", client, WorkerOptions{})

    // Verify keys match
    kb := NewKeyBuilder("test-queue", client)
    assert.Equal(t, "bull:test-queue:wait", kb.Wait())
    assert.Equal(t, "bull:test-queue:1", kb.Job("1"))
}
```

### 6.2 Manual Testing Checklist

- [ ] Start single-instance Redis
- [ ] Create Go queue, add job, verify key in Redis: `KEYS bull:*`
- [ ] Start Redis Cluster (docker-compose)
- [ ] Create Go queue, add job, verify key uses hash tags: `KEYS bull:*`
- [ ] Test ForceHashTags on single-instance
- [ ] Run all existing tests (should still pass)

### 6.3 Node.js Integration Testing

Create test script `tests/nodejs-compat/test.js`:
```javascript
const { Queue } = require('bullmq');

// Create queue (Node.js default - no hash tags)
const queue = new Queue('test-queue', {
  connection: { host: 'localhost', port: 6379 }
});

// Add job
await queue.add('test-job', { foo: 'bar' });

console.log('Job created by Node.js with keys:', await queue.getKeys());
// Expected: bull:test-queue:wait (no hash tags)
```

Then verify Go can consume:
```go
worker := bullmq.NewWorker("test-queue", singleInstanceClient, opts)
// Should pick up job created by Node.js
```

---

## 7. Migration Strategy

### 7.1 Who Needs to Migrate?

**NO MIGRATION NEEDED**:
- New deployments (start with v0.1.1)
- Go-only deployments planning to use v0.1.1
- Haven't deployed v0.1.0 yet (released <2 hours ago)

**MIGRATION NEEDED**:
- Existing v0.1.0 deployments (if any exist)
- Deployments with data in Redis using hash tag keys

### 7.2 Migration Options

**Option A: Redis RENAME (Zero Downtime)**
```bash
# Rename all keys from hash tag format to standard format
redis-cli --scan --pattern 'bull:{myqueue}:*' | while read key; do
  newkey=$(echo $key | sed 's/{myqueue}/myqueue/')
  redis-cli RENAME "$key" "$newkey"
done
```

**Option B: ForceHashTags Flag (No Migration)**
```go
// Keep using hash tags (v0.1.0 behavior)
queue := bullmq.NewQueue("myqueue", client, bullmq.QueueOptions{
    ForceHashTags: true,
})
worker := bullmq.NewWorker("myqueue", client, bullmq.WorkerOptions{
    ForceHashTags: true,
})
```

**Option C: Dual-Read Period**
- Not recommended (too complex)
- Check both key formats during transition

### 7.3 Migration Script

Create `scripts/migrate-v0.1.0-to-v0.1.1.sh`:
```bash
#!/bin/bash
# Migration script for v0.1.0 → v0.1.1
# Renames Redis keys from hash tag format to standard format

QUEUE_NAME="$1"
REDIS_HOST="${2:-localhost}"
REDIS_PORT="${3:-6379}"

if [ -z "$QUEUE_NAME" ]; then
  echo "Usage: $0 <queue-name> [redis-host] [redis-port]"
  exit 1
fi

echo "Migrating queue: $QUEUE_NAME"
echo "Redis: $REDIS_HOST:$REDIS_PORT"

# Find all keys with hash tags
keys=$(redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" --scan --pattern "bull:{$QUEUE_NAME}:*")

count=0
for key in $keys; do
  # Remove hash tags: bull:{myqueue}:wait → bull:myqueue:wait
  newkey=$(echo "$key" | sed "s/{$QUEUE_NAME}/$QUEUE_NAME/")

  # Rename key
  redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" RENAME "$key" "$newkey"

  count=$((count + 1))
  echo "Renamed: $key → $newkey"
done

echo "Migration complete: $count keys renamed"
```

---

## 8. Risk Analysis

### 8.1 Risks of Fixing

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Breaking existing v0.1.0 users | LOW (released 2h ago) | HIGH | Migration script + ForceHashTags option |
| New bugs in auto-detection | MEDIUM | MEDIUM | Comprehensive tests, fallback to hash tags on error |
| Performance impact | LOW | LOW | Detection happens once at initialization |
| Cluster validation breaks | LOW | MEDIUM | Keep existing cluster tests, add single-instance tests |

### 8.2 Risks of NOT Fixing

| Risk | Likelihood | Impact | Severity |
|------|-----------|--------|----------|
| Cross-language adoption failure | HIGH | CRITICAL | P0 |
| Silent job loss in production | HIGH | CRITICAL | P0 |
| Reputation damage | MEDIUM | HIGH | P1 |
| Fragmented ecosystem (two incompatible versions) | HIGH | HIGH | P0 |
| Support burden (debugging confusion) | HIGH | MEDIUM | P1 |

### 8.3 Recommendation

**FIX IMMEDIATELY** because:
1. v0.1.0 released <2 hours ago (minimal impact)
2. Cross-language compatibility is stated primary goal
3. Silent failures are worse than breaking changes
4. Simple fix with clear mitigation path
5. Risk of NOT fixing >> Risk of fixing

---

## 9. Rollout Plan

### 9.1 Version Strategy

**Option A**: Delete v0.1.0, replace with fixed version (REJECTED)
- Cons: Breaks Git history, looks unprofessional
- Cons: Anyone who pulled v0.1.0 has orphaned commit

**Option B**: v0.1.1 hotfix (RECOMMENDED)
- Keep v0.1.0 tag (preserve history)
- Create fix commit on same branch
- Tag as v0.1.1
- Update PR #1 to include both versions
- Release notes explain hotfix

**Option C**: v0.2.0 with breaking change
- Defer fix to next major version
- Document incompatibility in v0.1.0
- Cons: Delays compatibility, confusing versioning

### 9.2 Timeline

**Immediate** (Today):
- [x] Analysis complete (this document)
- [ ] Present plan to user for approval
- [ ] Implement fix (1 hour)
- [ ] Test fix (30 mins)
- [ ] Update docs (20 mins)
- [ ] Create v0.1.1 release
- [ ] Update PR #1

**Follow-up** (This week):
- [ ] Real Node.js integration test
- [ ] Performance benchmarks
- [ ] Stress testing

### 9.3 Communication

**PR #1 Update**:
```markdown
## 🚨 HOTFIX v0.1.1 Included

**Critical Bug Fixed**: Cross-language key incompatibility on single-instance Redis

v0.1.0 used hash tags unconditionally, breaking compatibility with Node.js BullMQ
on single-instance Redis. v0.1.1 fixes this by auto-detecting Redis mode:

- Single-instance: `bull:myqueue:wait` (matches Node.js)
- Cluster: `bull:{myqueue}:wait` (optimized for cluster)

**Migration**: If you deployed v0.1.0, see MIGRATION.md
```

**Release Notes v0.1.1**:
```markdown
# v0.1.1 - Critical Hotfix (2025-10-31)

## 🐛 Critical Bug Fix

Fixed cross-language compatibility issue where Go workers couldn't see jobs
created by Node.js BullMQ on single-instance Redis.

**Root Cause**: v0.1.0 always used hash tags `{queue-name}`, while Node.js
defaults to no hash tags on single-instance Redis.

**Fix**: Auto-detect Redis mode and use appropriate key format:
- Single-instance: `bull:myqueue:wait` (matches Node.js)
- Cluster: `bull:{myqueue}:wait` (cluster-optimized)

**Migration**: See MIGRATION.md if you deployed v0.1.0
```

---

## 10. Success Criteria

### 10.1 Acceptance Criteria

- [ ] Single-instance Redis: Keys have NO hash tags
- [ ] Redis Cluster: Keys have hash tags
- [ ] ForceHashTags option works
- [ ] All existing tests pass
- [ ] New compatibility tests pass
- [ ] Documentation updated
- [ ] Migration guide provided
- [ ] PR updated with hotfix notes

### 10.2 Verification Checklist

**Functionality**:
- [ ] Go queue + Go worker (single-instance) ✅
- [ ] Go queue + Go worker (cluster) ✅
- [ ] Node.js queue + Go worker (single-instance) ✅
- [ ] Go queue + Node.js worker (single-instance) ✅
- [ ] ForceHashTags=true on single-instance ✅

**Quality**:
- [ ] All unit tests pass
- [ ] All integration tests pass
- [ ] Cluster tests still pass
- [ ] No performance regression
- [ ] Code coverage maintained

**Documentation**:
- [ ] CLAUDE.md updated
- [ ] README updated
- [ ] MIGRATION.md created
- [ ] PR description updated
- [ ] Release notes written

---

## 11. Recommendation

**PROCEED WITH HOTFIX v0.1.1**

This is a **critical P0 bug** that breaks the primary use case (cross-language compatibility).
The fix is straightforward, low-risk, and v0.1.0 is too new to have production users.

**Next Step**: Get approval from user, then implement immediately.

**Estimated Total Time**: ~90 minutes
- Implementation: 30 mins
- Testing: 25 mins
- Documentation: 20 mins
- Release: 15 mins

**Alternative**: If you prefer to defer, document the incompatibility prominently and
fix in v0.2.0, but this delays the primary value proposition of the library.
