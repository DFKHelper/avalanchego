# Unified EVM Sync Package

## Overview

This package provides consolidated sync implementations for both **coreth** and **subnet-evm**, eliminating ~2,760 lines of duplicated code across handlers, client, and state sync components.

## Current Status (Week 2-3)

- ✅ Package structure and interfaces
- ✅ Config validation utilities (13 tests passing)
- ✅ Handler foundation (5 tests passing)
- ✅ **All three handlers consolidated** ✅

## Code Reduction

### Handler Consolidation (Completed Week 2-3)

**Leafs Request Handler:**
- **Before**: 1,002 LOC (532 coreth + 470 subnet-evm)
- **After**: 629 LOC (unified)
- **Savings**: 373 LOC (37% reduction)

**Code Request Handler:**
- **Before**: 190 LOC (95 coreth + 95 subnet-evm)
- **After**: 143 LOC (unified)
- **Savings**: 47 LOC (25% reduction)

**Block Request Handler:**
- **Before**: 240 LOC (120 coreth + 120 subnet-evm)
- **After**: 169 LOC (unified)
- **Savings**: 71 LOC (30% reduction)

**Total Handler Savings**: 491 LOC eliminated ✅

### Remaining Work

**Client (Week 3-4)**
- client.go: ~1,100 LOC savings

**State Syncer (Week 4-5)**
- state_syncer.go: ~900 LOC savings

### Total Progress
- **Handlers completed**: 491 LOC saved ✅
- **Remaining**: ~2,000 LOC (client + state syncer)
- **Total expected**: ~2,500 LOC reduction (revised from 2,760)

## Usage

```go
import "github.com/ava-labs/avalanchego/x/sync/evm/handlers"

// For coreth (with proofDBPool optimization)
handler := handlers.NewLeafsRequestHandler(
    trieDB,
    trieKeyLength,
    snapshotProvider,
    codec,
    stats,
    handlers.VersionCoreth,
)

// For subnet-evm (standard mode)
handler := handlers.NewLeafsRequestHandler(
    trieDB,
    trieKeyLength,
    snapshotProvider,
    codec,
    stats,
    handlers.VersionSubnetEVM,
)
```

## Next Steps

1. ~~Test leafs handler integration~~ (Ready for integration)
2. ~~Consolidate code_request.go and block_request.go~~ ✅ **COMPLETE**
3. Consolidate client implementation (Week 3-4)
4. Consolidate state syncer (Week 4-5)
5. Migration to both chains (Week 6)

## Handler Usage Examples

### Code Request Handler

```go
import "github.com/ava-labs/avalanchego/x/sync/evm/handlers"

handler := handlers.NewCodeRequestHandler(
    codeReader,  // ethdb.KeyValueReader
    codec,       // codec.Manager
    stats,       // CodeRequestStats interface
)
```

### Block Request Handler

```go
handler := handlers.NewBlockRequestHandler(
    blockProvider,  // BlockProvider interface
    codec,          // codec.Manager
    stats,          // BlockRequestStats interface
)
```
