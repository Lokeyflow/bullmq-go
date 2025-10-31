# Redis Cluster Integration Testing

This directory contains integration tests for Redis Cluster compatibility, validating that BullMQ Go works correctly in a distributed Redis environment.

## Overview

**Redis Cluster** distributes data across multiple nodes using consistent hashing with 16,384 hash slots. For multi-key operations (like BullMQ's Lua scripts), all keys must hash to the same slot. This is achieved using **hash tags** in key names.

## Prerequisites

- **Docker** or **Docker Desktop** installed
- **Docker Compose** (v1.27+ or Docker Compose V2)
- **Go 1.21+** for running tests

## Quick Start

### 1. Start Redis Cluster

```bash
# From the tests/integration directory
cd tests/integration
docker-compose -f docker-compose.cluster.yml up -d

# Verify cluster is running
docker-compose -f docker-compose.cluster.yml ps
```

Expected output:
```
NAME                    IMAGE           PORTS
bullmq-test-redis-1    redis:7-alpine  0.0.0.0:7001->6379/tcp, 0.0.0.0:17001->16379/tcp
bullmq-test-redis-2    redis:7-alpine  0.0.0.0:7002->6379/tcp, 0.0.0.0:17002->16379/tcp
bullmq-test-redis-3    redis:7-alpine  0.0.0.0:7003->6379/tcp, 0.0.0.0:17003->16379/tcp
```

### 2. Run Cluster Tests

```bash
# From repository root
cd ../..

# Run cluster tests
go test -v ./tests/integration -run TestRedisCluster

# Run specific test
go test -v ./tests/integration -run TestRedisClusterHashTags
go test -v ./tests/integration -run TestRedisClusterBullMQIntegration
```

### 3. Stop Redis Cluster

```bash
cd tests/integration
docker-compose -f docker-compose.cluster.yml down -v
```

The `-v` flag removes volumes, ensuring a clean state for next run.

## Test Coverage

### TestRedisClusterHashTags (T109, T110, T112)

Validates hash tag implementation and CROSSSLOT error handling:

1. **KeysWithHashTagsInSameSlot**: All BullMQ keys for a queue hash to the same slot
2. **MultiKeyLuaScriptExecution**: Multi-key Lua scripts execute without errors
3. **CrossSlotOperationsFail**: Keys without hash tags fail with CROSSSLOT error (negative test)
4. **BullMQWorkerInCluster**: Full producer/queue integration in cluster mode

**What's Validated**:
- All 14 BullMQ keys use `{queue-name}` hash tag
- Multi-key operations work atomically
- Keys without hash tags correctly fail in cluster mode

### TestRedisClusterBullMQIntegration (T111)

End-to-end BullMQ integration test:

1. Create queue with Redis Cluster client
2. Add job with priority and retry options
3. Verify all keys hash to same slot
4. Check cluster health and state
5. Validate job data storage

**What's Validated**:
- Producer API works with cluster client
- Job options (priority, attempts) stored correctly
- Cluster state remains healthy
- No CROSSSLOT errors during operations

## Cluster Architecture

### Node Configuration

- **3 master nodes** (minimum for Redis Cluster)
- **No replicas** (0 replicas for testing simplicity)
- **Hash slots**: 0-5460 (node 1), 5461-10922 (node 2), 10923-16383 (node 3)

### Port Mapping

| Node | Redis Port | Cluster Bus | Host Port |
|------|-----------|-------------|-----------|
| Node 1 | 6379 | 16379 | 7001 / 17001 |
| Node 2 | 6379 | 16379 | 7002 / 17002 |
| Node 3 | 6379 | 16379 | 7003 / 17003 |

### Network

- **Subnet**: 172.30.0.0/16
- **Node IPs**: 172.30.0.11, 172.30.0.12, 172.30.0.13
- **Driver**: bridge (isolated Docker network)

## Connecting to Cluster

### Via Go Client

```go
import "github.com/redis/go-redis/v9"

client := redis.NewClusterClient(&redis.ClusterOptions{
    Addrs: []string{
        "localhost:7001",
        "localhost:7002",
        "localhost:7003",
    },
})
defer client.Close()
```

### Via redis-cli

```bash
# Connect to node 1 in cluster mode
redis-cli -c -p 7001

# Check cluster status
127.0.0.1:7001> CLUSTER INFO
cluster_state:ok
cluster_slots_assigned:16384
cluster_known_nodes:3

# List cluster nodes
127.0.0.1:7001> CLUSTER NODES

# Check key slot
127.0.0.1:7001> CLUSTER KEYSLOT "bull:{myqueue}:wait"
(integer) 2331
```

## Troubleshooting

### Cluster fails to start

**Symptom**: Tests timeout waiting for cluster
**Solution**: Check Docker logs

```bash
docker-compose -f docker-compose.cluster.yml logs
```

Common issues:
- Port conflicts (7001-7003 already in use)
- Insufficient Docker resources (memory/CPU)
- Network conflicts with existing Docker networks

### CROSSSLOT errors during tests

**Symptom**: `CROSSSLOT Keys in request don't hash to the same slot`
**Cause**: Keys missing hash tags or incorrect KeyBuilder usage

**Debug**:
```bash
# Check which slot keys hash to
redis-cli -p 7001 CLUSTER KEYSLOT "bull:myqueue:wait"      # Wrong! Different slots
redis-cli -p 7001 CLUSTER KEYSLOT "bull:{myqueue}:wait"    # Correct! Same slot
```

### Tests hang during startup

**Symptom**: Test waits forever at "Starting Redis Cluster..."
**Solution**: Check Docker Compose is installed

```bash
# Check Docker Compose version
docker-compose version
# or
docker compose version

# If not installed, install Docker Compose
# https://docs.docker.com/compose/install/
```

### Port conflicts

**Symptom**: `Error starting userland proxy: listen tcp4 0.0.0.0:7001: bind: address already in use`
**Solution**: Stop conflicting services or change ports

```bash
# Check what's using the port
lsof -i :7001  # macOS/Linux
netstat -ano | findstr :7001  # Windows

# Kill the process or change ports in docker-compose.cluster.yml
```

## Skipping Cluster Tests

Cluster tests are automatically skipped when:

1. Running with `-short` flag:
   ```bash
   go test -short ./tests/integration
   ```

2. Environment variable set:
   ```bash
   SKIP_CLUSTER_TESTS=1 go test ./tests/integration
   ```

3. Docker/Docker Compose not available (automatic detection)

## CI/CD Integration

### GitHub Actions Example

```yaml
name: Cluster Tests

on: [push, pull_request]

jobs:
  cluster-tests:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Start Redis Cluster
        run: |
          cd tests/integration
          docker-compose -f docker-compose.cluster.yml up -d
          sleep 10

      - name: Run Cluster Tests
        run: go test -v ./tests/integration -run TestRedisCluster

      - name: Stop Redis Cluster
        if: always()
        run: |
          cd tests/integration
          docker-compose -f docker-compose.cluster.yml down -v
```

## Performance Notes

- **Startup time**: ~10 seconds (cluster initialization)
- **Test duration**: ~5 seconds (with warm cluster)
- **Memory usage**: ~100MB per node (~300MB total)
- **CPU usage**: Minimal (<5% on modern hardware)

## Further Reading

- [Redis Cluster Specification](https://redis.io/docs/reference/cluster-spec/)
- [Redis Cluster Tutorial](https://redis.io/docs/manual/scaling/)
- [BullMQ Redis Cluster Guide](https://docs.bullmq.io/guide/going-to-production/redis-cluster)
- [Go Redis Cluster Client](https://redis.uptrace.dev/guide/go-redis-cluster.html)
