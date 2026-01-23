# Unified EVM Sync Package

## Overview

This package provides consolidated sync implementations for both **coreth** and **subnet-evm**, eliminating ~2,760 lines of duplicated code across handlers, client, and state sync components.

## Current Status (Week 2)

- ✅ Package structure and interfaces
- ✅ Config validation utilities (13 tests passing)
- ✅ Handler foundation (5 tests passing)
- ✅ **Consolidated leafs request handler** (629 LOC, replaces ~1,000 LOC)

## Code Reduction

### Leafs Request Handler (Completed)
- **Before**: 1,002 LOC (532 coreth + 470 subnet-evm)
- **After**: 629 LOC (unified)
- **Savings**: 373 LOC (37% reduction)

### Remaining Handlers (Week 3)
- code_request.go: ~200 LOC savings
- block_request.go: ~200 LOC savings

### Client (Week 3-4)
- client.go: ~1,100 LOC savings

### Total Expected Savings
- **~2,760 LOC reduction** (from ~3,800 to ~1,040)

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

1. Test leafs handler integration
2. Consolidate code_request.go and block_request.go
3. Consolidate client implementation
4. Migration to both chains
