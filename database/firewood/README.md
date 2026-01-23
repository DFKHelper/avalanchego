# Firewood Database Adapter

**Status**: 🚧 **WORK IN PROGRESS** - Awaiting fork with iterator support

A drop-in replacement database adapter for AvalancheGo using Firewood, a high-performance merkle trie database.

---

## Overview

Firewood is a merkle trie database optimized for blockchain state storage. This adapter integrates Firewood as an alternative database backend for AvalancheGo, replacing LevelDB or PebbleDB.

**Key Benefits**:
- ✅ **Native merkle trie support** - Built-in proof generation
- ✅ **Versioned storage** - Historical state queries
- ✅ **Efficient pruning** - Optimized for blockchain use cases
- ✅ **Memory-mapped I/O** - High performance
- ⏳ **Iterator support** - Coming soon in fork

---

## Current Status

### Completed (40%)
- [x] Database adapter scaffold (`db.go`) - 323 LOC
- [x] Iterator wrapper (`iterator.go`) - 115 LOC
- [x] Configuration system (`config.go`) - 75 LOC
- [x] Test framework (`db_test.go`) - 95 LOC
- [x] Migration tool (`migrate.go`) - 330 LOC
- [x] CLI migration utility (`cmd/migrate/main.go`) - 150 LOC
- [x] Factory integration (`../factory/factory.go`) - Ready (commented)
- [x] Fork implementation guide (`FORK_IMPLEMENTATION_GUIDE.md`)
- [x] Integration test plan (`INTEGRATION_TEST_PLAN.md`)

### Pending (60%)
- [ ] Fork Firewood repository
- [ ] Implement iterator in Rust
- [ ] Add FFI bindings for iterator
- [ ] Uncomment adapter code
- [ ] Run unit tests
- [ ] Integration testing (5 phases)
- [ ] Production deployment

---

## Architecture

```
┌─────────────────────────────────────┐
│      AvalancheGo Database API       │
│    (database.Database interface)    │
└─────────────┬───────────────────────┘
              │
              v
┌─────────────────────────────────────┐
│     Firewood Adapter (db.go)        │
│  - Implements Database interface    │
│  - Wraps FFI calls                  │
│  - Error handling & logging         │
└─────────────┬───────────────────────┘
              │
              v
┌─────────────────────────────────────┐
│   Go FFI Bindings (forked repo)    │
│  github.com/YOUR-FORK/              │
│    firewood-go-ethhash/ffi          │
└─────────────┬───────────────────────┘
              │ CGO
              v
┌─────────────────────────────────────┐
│   Rust Firewood (forked repo)      │
│  github.com/YOUR-FORK/firewood      │
│  - Merkle trie implementation       │
│  - Iterator support (NEW)           │
└─────────────────────────────────────┘
```

---

## Files

### Core Adapter
- **db.go** - Main database implementation with TODOs for fork integration
- **iterator.go** - Iterator wrapper for database iteration
- **config.go** - Configuration options and defaults
- **batch.go** - (embedded in db.go) Batch operation implementation

### Testing
- **db_test.go** - Unit test framework with placeholders
- **INTEGRATION_TEST_PLAN.md** - 5-phase testing strategy

### Migration
- **migrate.go** - Library for migrating from LevelDB to Firewood
- **cmd/migrate/main.go** - CLI tool for database migration

### Documentation
- **README.md** - This file
- **FORK_IMPLEMENTATION_GUIDE.md** - Detailed fork implementation specs

---

## Configuration

```go
config := firewood.Config{
    CacheSizeBytes:       512 * 1024 * 1024,  // 512 MB cache
    FreeListCacheEntries: 1024,                // Free list cache size
    RevisionsInMemory:    10,                  // Historical revisions
    CacheStrategy:        "lru",               // LRU or LFU
}
```

### Configuration Options

| Option | Default | Description |
|--------|---------|-------------|
| `CacheSizeBytes` | 512 MB | In-memory node cache size |
| `FreeListCacheEntries` | 1024 | Free list allocation cache |
| `RevisionsInMemory` | 10 | Historical state revisions to keep |
| `CacheStrategy` | "lru" | Eviction policy: "lru" or "lfu" |

---

## Usage

### As Database Backend

Once the fork is ready, enable Firewood in your node configuration:

```json
{
  "db-type": "firewood",
  "db-config": {
    "cacheSizeBytes": 536870912,
    "revisionsInMemory": 10,
    "cacheStrategy": "lru"
  }
}
```

### Migration Tool

Migrate existing LevelDB database to Firewood:

```bash
# Build migration tool
go build -o migrate ./database/firewood/cmd/migrate

# Estimate migration time
./migrate -source /data/leveldb -dest /data/firewood -estimate

# Perform migration with verification
./migrate -source /data/leveldb -dest /data/firewood -verify -batch 10000
```

**Migration Options**:
- `-source` - Path to source database (required)
- `-dest` - Path to destination Firewood database (required)
- `-batch` - Batch size (default: 10000)
- `-verify` - Verify data after migration
- `-estimate` - Estimate time and exit
- `-sample` - Sample size for estimation (default: 1000)

---

## Development

### Building

```bash
# Install dependencies (once fork is ready)
go mod download

# Build
go build ./database/firewood/...

# Run tests
go test ./database/firewood/...
```

### Testing

```bash
# Unit tests
go test -v ./database/firewood/...

# With race detector
go test -race ./database/firewood/...

# Benchmarks
go test -bench=. ./database/firewood/...
```

---

## Integration Test Plan

See `INTEGRATION_TEST_PLAN.md` for the comprehensive 5-phase testing strategy:

1. **Unit Tests** (Week 6) - Interface compliance, basic operations
2. **Integration Tests** (Week 7) - Migration, state roots, bootstrap
3. **Performance Benchmarks** (Week 8) - Throughput, latency comparison
4. **Stress Testing** (Week 9) - 24h soak test, large datasets
5. **Production Validation** (Week 10) - Testnet and mainnet deployment

---

## Fork Implementation

To implement the Firewood fork with iterator support, see `FORK_IMPLEMENTATION_GUIDE.md`.

**Summary**:
1. Fork `github.com/ava-labs/firewood` and `github.com/ava-labs/firewood-go-ethhash`
2. Implement iterator in Rust (`db.rs`, `iterator.rs`)
3. Add FFI bindings (`ffi/database.go`)
4. Test iterator thoroughly
5. Update adapter code (uncomment TODOs)
6. Run integration tests

---

## Performance

**Expected Performance** (compared to LevelDB):

| Metric | Target | Rationale |
|--------|--------|-----------|
| Read throughput | Within 20% | Merkle trie overhead |
| Write throughput | Within 20% | Memory-mapped I/O helps |
| Iterator speed | Competitive | Native trie traversal |
| Memory usage | Equal or better | Configurable cache |
| Proof generation | **Native support** | Built-in merkle proofs |

---

## Troubleshooting

### Error: ErrIteratorNotImplemented

**Cause**: Firewood fork with iterator support not yet integrated

**Solution**:
1. Follow `FORK_IMPLEMENTATION_GUIDE.md` to implement iterator
2. Update `go.mod` to use forked repositories
3. Uncomment adapter code in `db.go` and `iterator.go`

### Error: CGO build failed

**Cause**: Firewood uses CGO and requires C compiler

**Solution**:
- Linux: `apt-get install build-essential`
- macOS: `xcode-select --install`
- Windows: Install MinGW or use WSL

### Migration fails with "batch write error"

**Cause**: Destination database may be read-only or disk full

**Solution**:
- Check disk space: `df -h`
- Verify write permissions
- Reduce batch size: `-batch 1000`

---

## Roadmap

### Phase 1: Fork Implementation (Weeks 1-3)
- [ ] Fork repositories on GitHub
- [ ] Implement Rust iterator
- [ ] Add FFI bindings
- [ ] Test iterator

### Phase 2: Adapter Completion (Weeks 4-5) ✅ COMPLETE
- [x] Database interface scaffold
- [x] Iterator wrapper
- [x] Configuration system
- [x] Migration tool

### Phase 3: Factory Integration (Week 6) ✅ COMPLETE
- [x] Update factory switch
- [x] Add configuration validation
- [x] Integration test plan

### Phase 4: Testing (Weeks 7-9)
- [ ] Unit tests
- [ ] Integration tests
- [ ] Performance benchmarks
- [ ] 24-hour soak test

### Phase 5: Production (Week 10)
- [ ] Testnet deployment
- [ ] Mainnet trial
- [ ] Performance validation
- [ ] Production rollout

---

## Contributing

### Code Structure
- Keep TODOs for fork-dependent code
- Return `ErrIteratorNotImplemented` for unimplemented features
- Maintain backwards compatibility with database interface

### Testing
- Add tests to `db_test.go`
- Follow integration test plan phases
- Benchmark against LevelDB baseline

### Documentation
- Update README.md with new features
- Keep FORK_IMPLEMENTATION_GUIDE.md current
- Document configuration options

---

## Resources

- **Firewood Repository**: https://github.com/ava-labs/firewood
- **Go FFI Bindings**: https://github.com/ava-labs/firewood-go-ethhash
- **Database Interface**: `database/database.go`
- **LevelDB Adapter**: `database/leveldb/` (reference implementation)

---

## License

Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
See the file LICENSE for licensing terms.

---

*Status*: Phase 2 & 3 complete (40% overall)
*Next milestone*: Fork implementation with iterator support
*Timeline*: 6 weeks remaining (Phases 1, 4-5)
