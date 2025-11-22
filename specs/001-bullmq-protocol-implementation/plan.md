# Implementation Plan: BullMQ Go Client Library

**Branch**: `001-bullmq-protocol-implementation` | **Date**: 2025-10-29 | **Spec**: [spec.md](./spec.md)

## Summary

Build a production-ready BullMQ client library for Go that provides full protocol compatibility with Node.js BullMQ. The library implements Worker API (job consumption), Producer API (job submission), and Queue Manager API (queue operations) using official BullMQ Lua scripts extracted from the Node.js repository. Core features include atomic job state transitions, lock heartbeat, stalled job detection, retry logic with exponential backoff, and comprehensive observability (metrics, structured logging, event streams).

## Technical Context

**Language/Version**: Go 1.21+ (requires generics and improved context handling)

**Primary Dependencies**:

- github.com/redis/go-redis/v9 (Redis client with cluster support)
- github.com/google/uuid (cryptographically secure lock tokens)
- github.com/stretchr/testify (testing framework)
- github.com/testcontainers/testcontainers-go (integration testing with Redis)

**Storage**: Redis 6.0+ (Lua script support, streams, cluster mode)

**Testing**: Go testing framework with testcontainers for integration tests, load testing with 10+ workers

**Target Platform**: Cross-platform (Linux, macOS, Windows) - library for server-side Go applications

**Project Type**: Single library package (no web/mobile components)

**Performance Goals**:

- Job pickup latency p95 < 10ms
- Lock heartbeat < 10ms per extension
- Stalled detection < 100ms per cycle
- Throughput ≥ 1000 jobs/second per worker
- Library overhead < 5% vs manual Redis commands

**Constraints**:

- BullMQ protocol compatibility (v5.62.0, commit 6a31e0a)
- Redis Cluster compatible (hash tags mandatory)
- At-least-once delivery semantics
- Idempotent job handlers required (user responsibility)
- Lock TTL 30s, heartbeat 15s, stalled check 30s
- Max job payload 10MB (JSON serialized)

**Scale/Scope**:

- Target: Production workloads with 10,000+ jobs/day
- Support: Multiple workers per queue, multiple queues per application
- Testing: Load tests with 10+ concurrent workers, 10,000+ jobs
- Compatibility: Node.js BullMQ v5.x interoperability

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### ✅ I. Protocol Compatibility

- **Status**: PASS (by design)
- **Evidence**: Lua scripts from official repo, hash tags, cross-language tests

### ✅ II. Performance & Resource Efficiency

- **Status**: PASS (targets defined)
- **Evidence**: <10ms pickup/heartbeat, <100ms stalled check, 1000+ jobs/s

### ✅ III. Operational Excellence

- **Status**: PASS (comprehensive observability)
- **Evidence**: Structured logging, Prometheus metrics, graceful shutdown

### ✅ IV. Error Handling & Resilience

- **Status**: PASS (comprehensive error handling)
- **Evidence**: Error categorization, retry logic, reconnection

### ✅ V. Test-Driven Development (TDD)

- **Status**: PASS (TDD mandatory)
- **Evidence**: Tests before code, >80% coverage, all test types defined

### ✅ VI. Observability & Monitoring

- **Status**: PASS (comprehensive metrics/logs)
- **Evidence**: Prometheus metrics, structured logs, Redis events

### ✅ VII. Resource Cleanup & Lifecycle Management

- **Status**: PASS (explicit resource management)
- **Evidence**: Goroutine cleanup, connection cleanup, lock release

### ✅ VIII. Public API Stability

- **Status**: PASS (semantic versioning)
- **Evidence**: Semver, CHANGELOG, deprecation warnings

**Overall Gate Status**: ✅ PASS - All constitutional principles satisfied, proceed to Phase 0

## Project Structure

### Documentation (this feature)

```
specs/001-bullmq-protocol-implementation/
├── spec.md              # Feature specification (complete)
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── redis-keys.md
│   ├── job-schema.json
│   ├── event-schema.json
│   └── lua-scripts.md
└── tasks.md             # Phase 2 output
```

### Source Code (repository root)

```
pkg/bullmq/              # Main library package
├── worker.go            # Worker API
├── producer.go          # Producer API
├── queue.go             # Queue management
├── job.go               # Job structures
├── keys.go              # Redis key builder
├── events.go            # Event emission
├── heartbeat.go         # Lock heartbeat
├── stalled.go           # Stalled detection
├── retry.go             # Retry logic
├── errors.go            # Error categorization
├── logger.go            # Logger interface
├── metrics/             # Optional metrics
│   └── metrics.go
└── scripts/             # Lua scripts
    ├── scripts.go
    └── *.lua

tests/
├── unit/
├── integration/
└── compatibility/

examples/
├── worker/
├── producer/
└── queue/
```

**Structure Decision**: Single library package. Standalone Go library in `pkg/bullmq/` with clear API separation. Tests by type with P0/P1 edge cases.

## Complexity Tracking

**Status**: No violations - all constitutional requirements satisfied.
