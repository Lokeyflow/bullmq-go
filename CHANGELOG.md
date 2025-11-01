# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.1] - 2025-11-01

### Fixed
- **CRITICAL**: Fixed cross-language compatibility issue with Node.js BullMQ on single-instance Redis
  - v0.1.0 always used hash tags `{queue-name}`, breaking interoperability with Node.js on single-instance Redis
  - Jobs created by Node.js producers were invisible to Go workers and vice versa
  - Root cause: Go always used `bull:{myqueue}:wait`, while Node.js used `bull:myqueue:wait` on single-instance Redis

### Added
- Automatic Redis mode detection (single-instance vs cluster)
  - KeyBuilder auto-detects client type via type assertion
  - Hash tags only used when connected to Redis Cluster
  - Matches Node.js BullMQ behavior in both single-instance and cluster modes
- Support for both `*redis.Client` and `*redis.ClusterClient` via `redis.Cmdable` interface
  - Updated Queue, Worker, EventEmitter, LogManager, ProgressUpdater, ScriptLoader, and Job structs
  - Enables seamless use of either single-instance or cluster Redis
- New test suite for auto-detection validation (5 tests, 19 sub-tests)
  - Verifies single-instance mode uses no hash tags
  - Verifies cluster mode uses hash tags
  - Tests explicit override with `NewKeyBuilderWithHashTags`
- Comprehensive documentation updates
  - Added "Redis Key Format Auto-Detection" section to CLAUDE.md
  - Updated README.md with "Automatic Redis Mode Detection" section
  - Explains cross-language compatibility improvements

### Changed
- **BREAKING (Internal API)**: Updated all Redis client fields from `*redis.Client` to `redis.Cmdable`
  - Public API unchanged - `NewQueue()` and `NewWorker()` still accept both client types
  - This change only affects users who were directly accessing internal client fields
- KeyBuilder signature: `NewKeyBuilder(name string, client interface{})` now requires client parameter
  - Use `NewKeyBuilderWithHashTags(name, useHashTags bool)` for explicit control

### Migration from v0.1.0
No migration needed for most users - the library auto-detects and adjusts automatically:
- Single-instance Redis: Works out-of-the-box, now compatible with Node.js
- Redis Cluster: Works out-of-the-box, hash tags applied automatically

## [0.1.0] - 2025-11-01

### Added
- Initial release of BullMQ-Go
- Full BullMQ protocol compatibility
- Worker API with configurable concurrency
- Producer API with priority, delay, and scheduling
- Queue management API (pause, resume, clean, remove)
- Atomic operations using official BullMQ Lua scripts
- Progress tracking and logging
- Retry logic with exponential backoff
- Stalled job detection and recovery
- Heartbeat-based lock extension
- Redis Cluster support with hash tags
- Comprehensive test suite (47 unit tests, 30 integration tests)
- Production-ready features:
  - Graceful shutdown
  - Error categorization (transient vs permanent)
  - Observability (structured logging, metrics)
  - Worker ID generation
  - Idempotent job handling

[0.1.1]: https://github.com/lokeyflow/bullmq-go/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/lokeyflow/bullmq-go/releases/tag/v0.1.0
