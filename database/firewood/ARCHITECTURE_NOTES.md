# Firewood Database Adapter - Architecture Notes

**Date**: January 24, 2026
**Issue**: Architectural mismatch between Firewood and database.Database interface

---

## The Mismatch

### database.Database Interface (Expected)
Simple key-value store with immediate operations:
```go
Put(key, value) error      // Write immediately
Get(key) (value, error)    // Read immediately
Delete(key) error          // Delete immediately
NewBatch() Batch           // Batch writes, commit with Write()
```

### Firewood API (Actual)
Merkle trie storage with proposal/commit pattern:
```go
// Reads from latest committed revision
Get(key) (value, error)

// Writes require proposal + commit cycle
Propose(keys, values) (*Proposal, error)
Commit(proposal) error

// Explicit revision management
GetRevision(root) (*Revision, error)
```

---

## Why This Matters

Firewood is designed for **blockchain state storage** where:
1. Multiple writes are batched into a proposal
2. Proposal is committed atomically
3. Each commit creates a new merkle tree revision
4. Old revisions can be kept for historical queries

database.Database is designed for **simple key-value storage** where:
1. Each Put() is independent
2. No explicit commit required
3. No revision history
4. No merkle proofs

---

## Adapter Strategies

### Option 1: Auto-Commit on Every Write (Simplest)
```go
func (db *Database) Put(key, value []byte) error {
    proposal, err := db.fw.Propose([][]byte{key}, [][]byte{value})
    if err != nil {
        return err
    }
    return db.fw.Commit(proposal)
}
```

**Pros**:
- Simple implementation
- Matches database.Database semantics exactly
- No state to track

**Cons**:
- Very inefficient (one merkle tree revision per Put!)
- Poor performance compared to LevelDB
- Doesn't leverage Firewood's batch capabilities

### Option 2: Batch-Based with Explicit Flush (Recommended)
```go
type Database struct {
    fw            *ffi.Database
    pendingBatch  *Batch  // Accumulate writes
    mu            sync.Mutex
}

func (db *Database) Put(key, value []byte) error {
    db.mu.Lock()
    defer db.mu.Unlock()
    db.pendingBatch.Put(key, value)

    // Auto-flush when batch gets large
    if db.pendingBatch.Size() > threshold {
        return db.flush()
    }
    return nil
}

func (db *Database) flush() error {
    proposal, err := db.fw.Propose(db.pendingBatch.Keys(), db.pendingBatch.Values())
    if err != nil {
        return err
    }
    err = db.fw.Commit(proposal)
    db.pendingBatch.Reset()
    return err
}
```

**Pros**:
- Better performance (fewer revisions)
- Leverages Firewood's batch capabilities
- Still provides reasonable Put() semantics

**Cons**:
- More complex implementation
- Need to handle flush timing
- Partial state in memory (risk on crash)

### Option 3: Background Commit Worker (Most Complex)
```go
type Database struct {
    fw         *ffi.Database
    writeChan  chan writeOp
    flushTimer *time.Timer
}

// Background goroutine commits periodically
func (db *Database) commitWorker() {
    ticker := time.NewTicker(100 * time.Millisecond)
    batch := NewBatch()

    for {
        select {
        case op := <-db.writeChan:
            batch.Put(op.key, op.value)
        case <-ticker.C:
            if batch.Len() > 0 {
                db.flush(batch)
                batch.Reset()
            }
        }
    }
}
```

**Pros**:
- Maximum performance
- Non-blocking writes
- Configurable commit frequency

**Cons**:
- Most complex
- Harder to reason about
- Goroutine management overhead

---

## Recommendation

**Start with Option 2 (Batch-Based with Auto-Flush)**

**Rationale**:
1. Good performance (batch multiple writes)
2. Reasonable complexity
3. Compatible with database.Database interface
4. Can be optimized later

**Implementation Plan**:
```go
type Database struct {
    fw            *ffi.Database
    pending       *batch    // Accumulate writes
    pendingMu     sync.Mutex
    flushSize     int       // Auto-flush threshold (default: 1000 ops)
    closed        atomic.Bool
}

// Put adds to pending batch, auto-flushes when large
func (db *Database) Put(key, value []byte) error {
    db.pendingMu.Lock()
    defer db.pendingMu.Unlock()

    if db.closed.Load() {
        return ErrClosed
    }

    if err := db.pending.Put(key, value); err != nil {
        return err
    }

    if db.pending.Len() >= db.flushSize {
        return db.flushLocked()
    }
    return nil
}

// Get reads from latest committed revision
func (db *Database) Get(key []byte) ([]byte, error) {
    // First check pending batch
    if val, ok := db.pending.Get(key); ok {
        return val, nil
    }

    // Then check committed state
    return db.fw.GetLatest(key)
}

// NewBatch returns explicit batch (no auto-flush)
func (db *Database) NewBatch() database.Batch {
    return newBatch(db)
}

// Close flushes pending and closes database
func (db *Database) Close() error {
    db.pendingMu.Lock()
    defer db.pendingMu.Unlock()

    if db.closed.Load() {
        return ErrClosed
    }

    // Flush any pending writes
    if db.pending.Len() > 0 {
        if err := db.flushLocked(); err != nil {
            return err
        }
    }

    db.closed.Store(true)
    return db.fw.Close()
}
```

---

## Read Consistency

**Challenge**: Pending writes not yet committed

**Solution**: Check pending batch before querying Firewood
```go
func (db *Database) Get(key []byte) ([]byte, error) {
    db.pendingMu.Lock()
    defer db.pendingMu.Unlock()

    // Check pending first (read-your-writes consistency)
    if val, exists := db.pending.Get(key); exists {
        if val == nil {
            return nil, database.ErrNotFound  // Pending delete
        }
        return val, nil
    }

    // Check committed state
    return db.fw.GetLatest(key)
}
```

---

## Iterator Considerations

**Challenge**: Iterator should see pending writes

**Solution**: Merge iterator pattern
```go
type iterator struct {
    fwIter      *ffi.Iterator  // Committed state
    pendingKeys [][]byte        // Pending writes
    pendingIdx  int
    current     *keyValuePair
}

func (it *iterator) Next() bool {
    // Merge committed + pending in sorted order
    // ...
}
```

---

## Performance Expectations

**With Option 2 (Auto-Flush @ 1000 ops)**:

| Operation | Expected Performance |
|-----------|---------------------|
| Single Put | ~10-100 μs (in-memory batch) |
| Flush (1000 ops) | ~50-100 ms (merkle tree commit) |
| Get (committed) | ~100-500 μs (trie lookup) |
| Get (pending) | ~1-10 μs (memory lookup) |
| Iterator | Comparable to LevelDB |

**Comparison to LevelDB**:
- Writes: Similar (both batch internally)
- Reads: Slightly slower (merkle trie vs LSM tree)
- Iterator: Similar
- Overhead: Pending batch tracking

---

## Alternative: Use Firewood for Trie Storage Only

**Consideration**: Maybe database.Database interface is wrong fit?

**Alternative Use Case**:
- Keep LevelDB for general database.Database needs
- Use Firewood specifically for trie storage (like graft/evm/firewood does)
- Don't try to make Firewood a drop-in database replacement

**Pros**:
- Use each database for its strength
- No architectural mismatch
- Simpler implementation

**Cons**:
- Doesn't achieve original goal (replace LevelDB with Firewood)
- More complex system (two databases)

**Decision**: Proceed with adapter for now, can revisit if performance is poor

---

## Next Steps

1. Implement Option 2 (Batch-Based Auto-Flush)
2. Write unit tests
3. Benchmark against LevelDB
4. If performance inadequate, consider:
   - Optimize flush threshold
   - Implement Option 3 (Background Worker)
   - Reconsider if Firewood is right fit for database.Database

---

**Conclusion**: Firewood can work with database.Database interface, but requires careful adapter design to bridge architectural differences. Batch-based auto-flush provides good balance of simplicity and performance.
