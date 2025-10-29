<!--
Sync Impact Report:
- Version change: 1.1.0 → 2.0.0
- Modified principles: ALL - Complete project redefinition from service to library
- Project scope change: LoKeyFlow Video Processing Service → BullMQ-Go Client Library
- Added sections:
  * Protocol Compatibility principle (replaces video-specific requirements)
  * Public API Stability principle
  * Cross-Language Compatibility principle
- Removed sections:
  * Video-specific performance targets
  * Service-specific operational requirements
  * Migration Protocol (not applicable to library)
- Modified sections:
  * Performance & Resource Efficiency - now library-focused
  * Quality Standards - library benchmarks instead of service SLAs
- Templates requiring updates:
  ✅ .specify/templates/spec-template.md - aligned with library requirements
  ✅ .specify/templates/plan-template.md - updated for library development
  ⚠️ .specify/templates/tasks-template.md - needs library-specific task categories
  ⚠️ .specify/templates/checklist-template.md - needs library quality gates
- Follow-up TODOs:
  * Update tasks-template.md to include cross-language compatibility test tasks
  * Update checklist-template.md for library release checklist
  * Create examples/ structure validation in quality gates
  * Add API compatibility check to CI/CD
-->

# BullMQ-Go Client Library Constitution

## Core Principles

### I. Protocol Compatibility

BullMQ protocol compatibility is the foundation of this library. Every operation
MUST produce Redis state identical to Node.js BullMQ to ensure seamless
interoperability.

**Mandatory Requirements**:

- All Lua scripts MUST be extracted from official BullMQ repository (pinned
  version)
- Redis key patterns MUST use hash tags `{queue-name}` for cluster compatibility
- Job state transitions MUST be atomic using official BullMQ Lua scripts
- Event stream format MUST match Node.js BullMQ exactly
- Cross-language compatibility tests MUST pass (Node.js ↔ Go interoperability)
- Any protocol deviation MUST be documented with justification and workaround

**Rationale**: The primary value proposition is enabling Go applications to
participate in BullMQ-based systems. Protocol incompatibility breaks this core
promise.

### II. Performance & Resource Efficiency

Performance is critical for a job queue library. Every operation MUST be
optimized for low latency and minimal resource overhead.

**Mandatory Requirements**:

- Job pickup latency MUST be < 10ms (p95)
- Lock heartbeat operations MUST be < 10ms per extension
- Stalled job detection MUST complete < 100ms per cycle
- Library overhead MUST be < 5% vs manual Redis commands
- Memory allocations MUST be minimized in hot paths
- Goroutine leaks MUST be prevented through proper lifecycle management
- Connection pooling MUST be used for Redis operations

**Rationale**: Job queue libraries are critical infrastructure. Poor performance
impacts all dependent services. Go's performance advantage must be realized.

### III. Operational Excellence

Library users depend on production-ready features. Observability, graceful
degradation, and recovery mechanisms are mandatory, not optional.

**Mandatory Requirements**:

- Structured logging with configurable log levels and pluggable logger interface
- Prometheus-compatible metrics exported via optional metrics package
- Graceful shutdown that waits for active jobs (configurable timeout)
- Automatic retry logic with exponential backoff for transient failures
- Stalled job detection and automatic requeue
- Heartbeat mechanism to maintain job ownership during long processing
- Context support for cancellation and timeout propagation

**Rationale**: Production systems require robust error handling and observability.
A library without these features cannot be used in serious production
environments.

### IV. Error Handling & Resilience

Failures are expected in distributed systems. The library MUST handle errors
gracefully and provide clear categorization for user-defined retry logic.

**Mandatory Requirements**:

- All Redis operations MUST have timeout bounds
- Transient errors MUST be detected and categorized (network, Redis connection)
- Permanent errors MUST be distinguished from transient (validation, auth)
- Error messages MUST be actionable with sufficient context
- Lock expiry MUST trigger automatic job requeue (stalled detection)
- Redis connection loss MUST trigger automatic reconnection
- User-provided processor functions MUST control retry behavior via error
  categorization

**Rationale**: Distributed systems fail regularly. The library must handle these
failures gracefully while giving users control over business logic retry
decisions.

### V. Test-Driven Development (TDD)

**Tests MUST be written BEFORE implementation.** This is non-negotiable. All
code MUST follow the Red-Green-Refactor cycle.

**Mandatory Requirements**:

- **TDD Cycle**: Write failing test → Implement minimal code → Refactor
- **Test First**: Unit, integration, and compatibility tests MUST exist and FAIL
  before implementation begins
- **Coverage**: All public API functions MUST have automated tests (target: >80%)
- **Test Types**:
  - Unit tests: Test individual functions/methods in isolation
  - Integration tests: Test Redis operations using testcontainers
  - Compatibility tests: Validate Node.js BullMQ interoperability
  - Load tests: Validate performance targets (1000+ jobs/second)
- **Edge Cases**: Concurrency, network failures, Redis errors MUST be tested
  explicitly
- **Quality Gates**: All tests MUST pass before merge; no exceptions
- **CI/CD**: Automated test runs on every commit with coverage reports

**Rationale**: TDD ensures API is usable, catches bugs early, serves as living
documentation, and prevents regressions. A library without tests is unusable.

### VI. Observability & Monitoring

Library users MUST be able to monitor and debug job processing. Metrics, logs,
and debugging tools are first-class requirements.

**Mandatory Requirements**:

- Prometheus-compatible metrics via optional `pkg/bullmq/metrics` package
- Key metrics: job latency (p50, p95, p99), throughput, queue lengths, error
  rate, stalled jobs, heartbeat success/failure
- Structured logging with pluggable logger interface (compatible with zerolog,
  zap, logrus)
- Job lifecycle events emitted to Redis streams (waiting, active, progress,
  completed, failed, stalled)
- Debug logging available for troubleshooting (disabled by default)
- Memory profiling support via pprof endpoints (example provided)

**Rationale**: Library users cannot fix what they cannot see. Observability
enables debugging, performance optimization, and operational confidence.

### VII. Resource Cleanup & Lifecycle Management

Resources MUST be managed explicitly. Goroutines, connections, and locks MUST
be cleaned up reliably to prevent leaks.

**Mandatory Requirements**:

- Goroutines MUST be stopped via context cancellation on shutdown
- Redis connections MUST be closed on worker/queue disposal
- Job locks MUST be released on completion, failure, or context cancellation
- Defer patterns MUST guarantee cleanup in all code paths
- Worker Stop() MUST wait for active jobs with configurable timeout
- Heartbeat goroutines MUST stop when job completes or worker shuts down
- Memory leaks MUST be validated through load tests (10,000+ jobs)

**Rationale**: Resource leaks cause gradual degradation in long-running
applications. Libraries must be leak-free by design.

### VIII. Public API Stability

Library users depend on stable APIs. Breaking changes MUST be minimized and
clearly communicated via semantic versioning.

**Mandatory Requirements**:

- Semantic versioning MUST be strictly followed (MAJOR.MINOR.PATCH)
- Public API changes require MAJOR version bump
- New features require MINOR version bump
- Bug fixes require PATCH version bump
- Deprecation warnings MUST be provided for 2 minor versions before removal
- CHANGELOG.md MUST document all breaking changes with migration guide
- Go module versioning MUST follow Go conventions (v2+)

**Rationale**: Breaking changes disrupt users and damage trust. Stability and
clear communication are essential for library adoption.

## Quality Standards

### Performance Benchmarks

All performance targets MUST be validated through automated load testing before
release. Targets are requirements, not suggestions.

**Required Baselines**:

- Job pickup latency: p95 < 10ms (1000 jobs, 10 concurrent workers)
- Lock heartbeat: < 10ms per extension
- Stalled job detection: < 100ms per cycle
- Library overhead: < 5% vs manual Redis commands
- Throughput: ≥ 1000 jobs/second per worker (10 concurrent processors)
- Memory per worker: < 50 MB baseline
- Goroutine count: Bounded (no leaks after 10,000 jobs)

### Code Quality Gates

Code quality is enforced through automated checks. The following gates MUST pass
before merge:

- All tests pass (unit, integration, compatibility, load)
- Test coverage ≥ 80% for `pkg/bullmq/` package
- Go Report Card grade A
- golangci-lint passes with no errors
- No race conditions detected (`go test -race`)
- Benchmarks show no performance regressions (±5% variance acceptable)
- API documentation complete (godoc for all public functions)
- Examples compile and run successfully

### Cross-Language Compatibility

Node.js BullMQ compatibility MUST be validated through automated tests.

**Required Validations**:

- Node.js producer → Go worker: Jobs processed correctly
- Go producer → Node.js worker: Jobs processed correctly
- Shadow test: Node.js + Go workers process same queue concurrently without
  conflicts
- Redis state format matches Node.js BullMQ (key inspection, HGETALL comparison)
- Event stream format matches Node.js BullMQ (XRANGE comparison)

## Development Workflow

### Specification-First Development

Implementation follows specification, not the reverse. Features begin with clear
requirements, not code.

**Process**:

1. Write specification with problem statement, functional requirements, and
   success criteria
2. Define public API interfaces and usage examples
3. Validate specification completeness (no [NEEDS CLARIFICATION] markers)
4. Obtain approval on specification before implementation
5. Write failing tests for all requirements (TDD)
6. Implement according to plan
7. Validate against success criteria (tests pass, benchmarks meet targets)
8. Document in README and examples/
9. Release with CHANGELOG entry

### Release Protocol

Library releases follow strict quality and communication standards.

**Process**:

1. Bump version according to semantic versioning
2. Update CHANGELOG.md with changes (breaking changes, features, fixes)
3. Run full test suite (unit, integration, compatibility, load)
4. Run benchmarks and validate no performance regressions
5. Update documentation (README, godoc, examples)
6. Create Git tag (e.g., `v0.2.0`)
7. Publish to GitHub releases with release notes
8. (Optional) Announce in Go community forums, Reddit r/golang

**Pre-1.0 Note**: API is unstable before v1.0.0. Breaking changes allowed with
MINOR version bumps.

**Post-1.0 Promise**: API stability guaranteed. Breaking changes only with MAJOR
version bumps.

## Governance

This constitution governs all development activities for the BullMQ-Go library.
Any deviation MUST be explicitly documented with justification and approved
through the amendment process.

### Amendment Process

1. Propose amendment with rationale and impact analysis
2. Update version according to semantic versioning:
   - MAJOR: Backward-incompatible principle changes or removals
   - MINOR: New principles or material expansions
   - PATCH: Clarifications, wording improvements, typo fixes
3. Update dependent templates and documentation
4. Document in Sync Impact Report
5. Commit with descriptive message (e.g., `docs: amend constitution to v2.1.0`)

### Compliance Review

All specifications, plans, and implementations MUST be reviewed for
constitutional compliance. Reviewers MUST verify:

- Protocol compatibility is maintained (Lua scripts, key patterns, events)
- Performance targets are defined and validated through tests
- Operational excellence requirements are implemented (logging, metrics,
  graceful shutdown)
- Error handling and resilience patterns follow best practices
- TDD discipline is followed (tests written first, coverage ≥80%)
- Public API changes follow semantic versioning
- Documentation is complete (godoc, README, examples)
- Cross-language compatibility is validated

### Version History

**Version**: 2.0.0 | **Ratified**: 2025-10-29 | **Last Amended**: 2025-10-29

**Changelog**:
- **2.0.0** (2025-10-29): Complete project redefinition - Changed from LoKeyFlow
  Video Processing Service to BullMQ-Go Client Library. Added protocol
  compatibility, public API stability, and cross-language compatibility
  principles. Updated all requirements for library context.
- **1.1.0** (2025-10-17): Made TDD explicit and obligatory - Section V now
  requires tests written BEFORE implementation with Red-Green-Refactor cycle
- **1.0.0** (2025-10-16): Initial constitution with 7 core principles
