# EVM Sync - Unified State Synchronization Package

**Status:** Week 1/6 - Package structure created
**Purpose:** Consolidate duplicate sync code between coreth and subnet-evm

---

## Overview

This package provides a unified state synchronization implementation that supports both:
- **Coreth:** Simple blocking pattern (`ModeBlocking`)
- **Subnet-EVM:** Advanced async pattern with stuck detection and restart (`ModeAsync`)

**Goal:** Reduce ~3,800 LOC of duplication to a single shared codebase.

---

## Package Structure

```
x/sync/evm/
├── interfaces.go       # Core interfaces and configuration
├── handlers/          # Request handlers (block, code, leafs) - Week 2
├── client/           # Client implementation with peer selection - Week 3
└── statesync/        # State sync coordinator - Weeks 4-5
```

---

## Current Status

### ✅ Completed (Week 1)
- Package structure created
- Interfaces defined (`StateSyncer`, `SyncConfig`, `SyncMode`)
- Configuration system with mode-specific features
- Retry policy framework

### ⏳ In Progress (Week 2)
- Handler consolidation
  - `leafs_request.go` (532 LOC coreth, 470 LOC subnet-evm, ~89% overlap)
  - `code_request.go`
  - `block_request.go`

### 📋 Planned (Weeks 3-6)
- Client consolidation (Week 3)
- Optional features: stuck detection, restart support (Weeks 4-5)
- Migration of coreth and subnet-evm to use shared package (Week 6)

---

## Usage

### Example: Coreth (Simple Blocking)

```go
import "github.com/ava-labs/avalanchego/x/sync/evm"

// Create config for simple blocking mode
config := evmsync.DefaultConfig(evmsync.ModeBlocking)
config.WorkerCount = 8
config.RequestSize = 1024

// Create syncer
syncer := evmsync.NewStateSyncer(config, client, db, trieDB)

// Sync blocks until complete
if err := syncer.Sync(ctx, rootHash); err != nil {
    log.Error("sync failed", "err", err)
}
```

### Example: Subnet-EVM (Advanced Async)

```go
import "github.com/ava-labs/avalanchego/x/sync/evm"

// Create config for async mode with advanced features
config := evmsync.DefaultConfig(evmsync.ModeAsync)
config.EnableStuckDetector = true
config.EnableRestart = true
config.MaxRestartAttempts = 5

// Create syncer
syncer := evmsync.NewStateSyncer(config, client, db, trieDB)

// Start in background
if err := syncer.Start(ctx, rootHash); err != nil {
    log.Error("failed to start sync", "err", err)
}

// Do other work...

// Wait for completion
if err := syncer.Wait(); err != nil {
    log.Error("sync failed", "err", err)
}
```

---

## Design Principles

### 1. Configurable Patterns
- Single codebase supports both simple and advanced use cases
- No breaking changes for existing implementations
- Feature flags enable optional functionality

### 2. Backward Compatibility
- Coreth keeps simple blocking behavior
- Subnet-EVM keeps advanced features (stuck detection, restart)
- Gradual migration path

### 3. Code Reuse
- 95% of handler code is identical → single implementation
- 85% of client code is shared → single implementation with config
- 40% of statesync code is shared → factored common components

---

## Duplication Analysis

### Handlers (~1,800 LOC savings)

| Handler | Coreth LOC | Subnet-EVM LOC | Overlap % | Savings |
|---------|------------|----------------|-----------|---------|
| leafs_request.go | 532 | 470 | 89% | ~450 |
| code_request.go | ~400 | ~380 | 90% | ~350 |
| block_request.go | ~450 | ~430 | 92% | ~400 |
| **Subtotal** | **~1,382** | **~1,280** | **~90%** | **~1,200** |

### Client (~1,500 LOC savings)

| Component | Coreth LOC | Subnet-EVM LOC | Overlap % | Savings |
|-----------|------------|----------------|-----------|---------|
| client.go | 526 | 620 | 85% | ~450 |
| leaf_syncer.go | ~400 | ~450 | 80% | ~350 |
| stats | ~300 | ~350 | 90% | ~300 |
| **Subtotal** | **~1,226** | **~1,420** | **~85%** | **~1,100** |

### StateSyncer (~900 LOC savings)

| Component | Coreth LOC | Subnet-EVM LOC | Overlap % | Savings |
|-----------|------------|----------------|-----------|---------|
| state_syncer.go | 451 | 921 | 40% | ~180 |
| trie_segments.go | ~300 | ~350 | 60% | ~180 |
| Other | ~200 | ~250 | 50% | ~100 |
| **Subtotal** | **~951** | **~1,521** | **~48%** | **~460** |

**Total Estimated Savings: ~2,760 LOC** (conservative estimate)

---

## Migration Plan

### Phase 1: Extract Common Components (Weeks 2-3)
1. Create shared handlers with version parameter
2. Create shared client with config-based differences
3. Unit tests for each component

### Phase 2: Add Optional Features (Weeks 4-5)
1. Stuck detector (optional, for ModeAsync)
2. Restart support (optional, for ModeAsync)
3. Enhanced monitoring and metrics

### Phase 3: Migration (Week 6)
1. Update coreth to use `x/sync/evm` (ModeBlocking)
2. Update subnet-evm to use `x/sync/evm` (ModeAsync)
3. Comprehensive regression testing
4. Performance validation

---

## Testing Strategy

### Unit Tests
- [ ] Interface implementation tests
- [ ] Config validation tests
- [ ] Mode switching tests
- [ ] Retry policy tests

### Integration Tests
- [ ] Coreth sync compatibility (ModeBlocking)
- [ ] Subnet-EVM sync compatibility (ModeAsync)
- [ ] Stuck detection functionality
- [ ] Restart support functionality

### Performance Tests
- [ ] Sync speed within 5% of original
- [ ] Memory usage comparable
- [ ] No regressions in error handling

---

## Contributing

### Adding New Features
1. Determine if feature is mode-specific or shared
2. Add configuration option if needed
3. Implement with backward compatibility
4. Add tests
5. Update documentation

### Consolidating Code
1. Identify duplication between coreth and subnet-evm
2. Extract common logic with parameters
3. Add version/config flags for differences
4. Migrate both implementations
5. Validate no regressions

---

## References

### Related Packages
- `graft/coreth/sync/` - Original coreth implementation
- `graft/subnet-evm/sync/` - Original subnet-evm implementation
- `database/` - Database interface
- `codec/` - Message encoding/decoding

### Documentation
- [Implementation Plan](../../../docs/WEEK-1-PROGRESS-SUMMARY.md)
- [Deployment Guide](../../../docs/DEPLOYMENT-GUIDE.md)

---

**Package Version:** 0.1.0 (Week 1)
**Last Updated:** 2026-01-23
**Next Milestone:** Handler consolidation (Week 2)
