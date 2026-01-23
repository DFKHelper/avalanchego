# Firewood Integration Test Plan

**Purpose**: Comprehensive testing strategy for Firewood database integration

**Prerequisites**:
- Firewood fork with iterator support complete
- FFI bindings implemented and tested
- Database adapter uncommented and functional

---

## Phase 1: Unit Tests (Week 6)

### 1.1 Database Interface Compliance

**File**: `database/firewood/db_test.go`

**Tests**:
```go
func TestDatabaseInterface(t *testing.T)
    // Verify Database implements database.Database

func TestBasicOperations(t *testing.T)
    // Test Put, Get, Has, Delete
    // Verify key-value storage works correctly

func TestNotFound(t *testing.T)
    // Verify ErrNotFound returned for missing keys

func TestDelete(t *testing.T)
    // Verify deletion removes key
    // Verify Has returns false after delete

func TestOverwrite(t *testing.T)
    // Put same key twice with different values
    // Verify latest value is returned
```

### 1.2 Batch Operations

```go
func TestBatchBasic(t *testing.T)
    // Create batch, add Put/Delete operations
    // Write batch, verify changes applied

func TestBatchAtomic(t *testing.T)
    // Create batch with multiple operations
    // Verify all-or-nothing semantics

func TestBatchReset(t *testing.T)
    // Create batch, add operations
    // Reset, verify operations cleared

func TestBatchReplay(t *testing.T)
    // Create batch, replay to another database
    // Verify operations replayed correctly
```

### 1.3 Iterator Tests

```go
func TestIteratorEmpty(t *testing.T)
    // Iterate over empty database
    // Verify Next() returns false immediately

func TestIteratorSingleKey(t *testing.T)
    // Put one key-value pair
    // Iterate, verify one item returned

func TestIteratorOrdering(t *testing.T)
    // Put keys in random order
    // Iterate, verify keys returned in sorted order

func TestIteratorWithStart(t *testing.T)
    // Put multiple keys
    // Create iterator with start key
    // Verify iteration begins at correct position

func TestIteratorWithPrefix(t *testing.T)
    // Put keys with different prefixes
    // Create prefix iterator
    // Verify only matching keys returned

func TestIteratorRelease(t *testing.T)
    // Create iterator, call Release()
    // Verify multiple Release() calls don't crash
    // Verify operations after Release() are no-op
```

### 1.4 Edge Cases

```go
func TestEmptyKey(t *testing.T)
    // Put with nil/empty key
    // Verify behavior matches LevelDB

func TestEmptyValue(t *testing.T)
    // Put with nil/empty value
    // Verify retrieval returns nil or empty slice

func TestLargeKey(t *testing.T)
    // Put with very large key (1MB)
    // Verify storage and retrieval work

func TestLargeValue(t *testing.T)
    // Put with very large value (100MB)
    // Verify storage and retrieval work
```

### 1.5 Concurrency Tests

```go
func TestConcurrentReads(t *testing.T)
    // Multiple goroutines reading simultaneously
    // Verify no data races (run with -race)

func TestConcurrentWrites(t *testing.T)
    // Multiple goroutines writing different keys
    // Verify all writes succeed

func TestConcurrentIterators(t *testing.T)
    // Multiple goroutines creating iterators
    // Verify correct behavior
```

### 1.6 Resource Management

```go
func TestClose(t *testing.T)
    // Open database, close it
    // Verify subsequent operations fail with ErrClosed

func TestDoubleClose(t *testing.T)
    // Close database twice
    // Verify second close returns ErrClosed

func TestMemoryLeaks(t *testing.T)
    // Run with CGO memory leak detection
    // Perform operations, verify no leaks
```

### 1.7 Health Checks

```go
func TestHealthCheck(t *testing.T)
    // Call HealthCheck on open database
    // Verify returns healthy status

func TestHealthCheckClosed(t *testing.T)
    // Close database, call HealthCheck
    // Verify returns error
```

---

## Phase 2: Integration Tests (Week 7)

### 2.1 Database Migration

**File**: `database/firewood/migration_test.go`

```go
func TestMigrateFromLevelDB(t *testing.T)
    // Create LevelDB with test data
    // Migrate to Firewood
    // Verify all data transferred correctly

func TestMigrateLargeDatabase(t *testing.T)
    // Migrate 1GB+ database
    // Verify data integrity
    // Measure migration time
```

### 2.2 State Root Verification

**File**: `database/firewood/stateroot_test.go`

```go
func TestStateRootMatchesLevelDB(t *testing.T)
    // Bootstrap P-chain with 1K blocks using Firewood
    // Bootstrap same chain with LevelDB
    // Verify state roots match exactly

func TestStateRootConsistency(t *testing.T)
    // Multiple bootstrap runs
    // Verify deterministic state roots
```

### 2.3 Chain Bootstrap Tests

```go
func TestBootstrapPChain(t *testing.T)
    // Bootstrap P-chain using Firewood
    // Verify completion without errors
    // Compare performance to LevelDB

func TestBootstrapCChain(t *testing.T)
    // Bootstrap C-chain (EVM) using Firewood
    // Verify EVM state storage works
    // Compare with LevelDB baseline

func TestBootstrapXChain(t *testing.T)
    // Bootstrap X-chain using Firewood
    // Verify UTXO storage works
```

---

## Phase 3: Performance Benchmarks (Week 8)

### 3.1 Basic Operations

**File**: `database/firewood/bench_test.go`

```go
func BenchmarkPut(b *testing.B)
    // Benchmark Put operations
    // Various key/value sizes
    // Compare with LevelDB

func BenchmarkGet(b *testing.B)
    // Benchmark Get operations
    // Sequential and random access
    // Compare with LevelDB

func BenchmarkIterator(b *testing.B)
    // Benchmark iterator traversal
    // Full database scan
    // Compare with LevelDB

func BenchmarkBatch(b *testing.B)
    // Benchmark batch writes
    // Various batch sizes (10, 100, 1000, 10000)
    // Compare with LevelDB
```

### 3.2 Real-World Scenarios

```go
func BenchmarkBlockchainWrite(b *testing.B)
    // Simulate blockchain write pattern
    // Batch writes of blocks + state updates
    // Measure throughput (blocks/sec)

func BenchmarkStateQuery(b *testing.B)
    // Simulate state query pattern
    // Random account lookups
    // Measure latency (μs)
```

### 3.3 Memory Usage

```go
func BenchmarkMemoryUsage(b *testing.B)
    // Monitor memory during operations
    // Verify no memory leaks
    // Compare with LevelDB baseline
```

---

## Phase 4: Stress Testing (Week 9)

### 4.1 Soak Test

**Duration**: 24 hours minimum

**Test**: `TestSoakTest24Hours(t *testing.T)`

**Operations**:
- Continuous read/write operations
- Random key-value pairs
- Periodic iterator scans
- Monitor memory usage every 5 minutes
- Verify no crashes or errors

**Success Criteria**:
- Zero crashes
- Memory usage stable (no leaks)
- Performance consistent (no degradation)

### 4.2 Large Dataset Test

**Test**: `TestLargeDataset(t *testing.T)`

**Operations**:
- Insert 100M key-value pairs
- Total size: ~50GB
- Measure write throughput
- Measure read performance
- Verify data integrity

### 4.3 Concurrent Stress Test

**Test**: `TestConcurrentStress(t *testing.T)`

**Operations**:
- 100 concurrent goroutines
- Mixed read/write/iterate operations
- Run for 1 hour
- Verify no data races (run with -race)
- Verify data consistency

---

## Phase 5: Production Validation (Week 10)

### 5.1 Testnet Deployment

**Environment**: Fuji testnet

**Process**:
1. Deploy node with Firewood database
2. Bootstrap from scratch
3. Monitor for 7 days
4. Compare metrics with LevelDB nodes

**Metrics to Track**:
- Bootstrap time
- Memory usage
- Disk I/O
- CPU usage
- Sync speed (blocks/sec)
- Error rate

### 5.2 Mainnet Trial

**Environment**: Mainnet (single node, non-validator)

**Process**:
1. Deploy non-critical node with Firewood
2. Bootstrap from scratch
3. Monitor for 14 days
4. Gradually increase to validator node

**Success Criteria**:
- State roots match existing validators
- Performance within 20% of LevelDB
- Zero corruption errors
- Stable memory usage

---

## Acceptance Criteria

### Must Pass (Blocking)
- ✅ All unit tests passing
- ✅ State roots match LevelDB exactly
- ✅ 24-hour soak test with zero crashes
- ✅ No memory leaks detected
- ✅ Performance within 20% of LevelDB

### Should Pass (Important)
- ✅ 7-day testnet validation successful
- ✅ Iterator performance competitive
- ✅ Batch write performance equal or better
- ✅ Memory usage equal or better

### Nice to Have (Optional)
- ⭐ Performance better than LevelDB
- ⭐ Memory usage significantly lower
- ⭐ Faster bootstrap times
- ⭐ Native merkle proof support

---

## Rollback Plan

If any critical test fails:

1. **Immediate**: Disable Firewood in factory (comment out case)
2. **Short-term**: Document failure, fix root cause
3. **Re-test**: Run full test suite again
4. **Decision**: Go/no-go based on results

**Critical Failure Examples**:
- State root mismatch
- Data corruption
- Memory leak
- Crash during soak test

---

## Test Execution Timeline

| Week | Phase | Focus | Duration |
|------|-------|-------|----------|
| 6 | Unit Tests | Interface compliance, basic ops | 5 days |
| 7 | Integration | Migration, state roots, bootstrap | 5 days |
| 8 | Performance | Benchmarks, optimization | 5 days |
| 9 | Stress | 24h soak, large dataset, concurrency | 7 days |
| 10 | Production | Testnet (7 days) + Mainnet trial | 14 days |

**Total**: ~36 days (overlap between phases)

---

## Success Metrics Summary

- **Correctness**: State roots match LevelDB (100% accuracy)
- **Performance**: Within 20% of LevelDB (acceptable) or better (ideal)
- **Stability**: 24+ hours soak test, zero crashes
- **Memory**: Equal or better than LevelDB
- **Production**: 7+ days testnet, 14+ days mainnet without issues

---

*Created*: Week 5 (January 2026)
*Status*: Test plan ready for Phase 1 execution
*Next*: Fork Firewood, implement iterator, begin unit tests
