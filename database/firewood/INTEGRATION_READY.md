# Firewood Integration - Ready to Proceed

**Date**: January 24, 2026
**Status**: All prerequisites complete, ready for integration

---

## Summary

After forking and investigating the Firewood repositories, we've confirmed that:

1. ✅ **Iterator already exists** - No implementation needed
2. ✅ **FFI bindings already available** - `github.com/ava-labs/firewood-go-ethhash/ffi v0.0.18`
3. ✅ **Already in production** - Used by `graft/evm/firewood/triedb.go`
4. ✅ **Go module already imported** - Present in go.mod

**Conclusion**: We can proceed directly with integration using existing upstream packages. No need to use forks!

---

## Integration Plan

### Phase 1: Update database/firewood/db.go ✅ READY

**Current State**: Placeholder implementation returning `ErrIteratorNotImplemented`

**Target State**: Full implementation using `ffi.Database`

**Changes Required**:
1. Import `github.com/ava-labs/firewood-go-ethhash/ffi`
2. Replace placeholder Database struct with real ffi.Database
3. Implement all database.Database interface methods:
   - Has(key) bool
   - Get(key) []byte
   - Put(key, value)
   - Delete(key)
   - NewIterator() - **KEY FEATURE**
   - NewBatch()
   - Close()
   - HealthCheck()
   - Compact()

4. Update iterator.go to use actual FFI iterator

**Reference Implementation**: `graft/evm/firewood/triedb.go` (lines 1-100)

### Phase 2: Configuration Integration

**Current**: Placeholder Config struct in config.go
**Target**: Map to ffi.DatabaseConfig

**Example from triedb.go**:
```go
type TrieDBConfig struct {
    CacheSizeBytes       uint
    FreeListCacheEntries uint
    RevisionsInMemory    uint
    CacheStrategy        ffi.CacheStrategy
}
```

### Phase 3: Iterator Implementation

**Current**: Placeholder returning ErrIteratorNotImplemented
**Target**: Use ffi iterator functions

**FFI Functions Available**:
- `fwd_iter_on_revision()` - Create iterator
- `fwd_iter_next()` - Advance iterator
- `fwd_free_iterator()` - Free iterator

### Phase 4: Testing

**Unit Tests**:
- Test all database operations
- Test iterator traversal
- Test batch operations
- Test concurrent access

**Integration Tests**:
- Follow INTEGRATION_TEST_PLAN.md
- 5 phases from unit → production

---

## Decision: Use Upstream vs Fork

### Recommendation: **Use Upstream Package**

**Rationale**:
1. Iterator support already exists
2. Already in production use (graft/evm/firewood/)
3. No changes needed to Firewood codebase
4. Simpler maintenance (track upstream releases)

**Forks Created**: Keep as backup, but not needed for current work

**Action**: Proceed with integration using:
```go
import "github.com/ava-labs/firewood-go-ethhash/ffi"
```

---

## Timeline (Revised)

| Phase | Task | Duration | Status |
|-------|------|----------|--------|
| 1 | Fork repositories | ✅ Complete | Done |
| 2 | Verify iterator exists | ✅ Complete | Done |
| 3 | **Update db.go** | 2-4 hours | Next |
| 4 | **Update iterator.go** | 1-2 hours | Next |
| 5 | Unit tests | 4-6 hours | Pending |
| 6 | Integration tests | 1-2 days | Pending |
| 7 | Performance benchmarks | 1-2 days | Pending |
| 8 | Production deployment | 3-5 days | Pending |

**Total**: 1-2 weeks (vs original 10 weeks!)

---

## Implementation Reference

### Existing Working Example

**File**: `graft/evm/firewood/triedb.go`

**Key Patterns**:
```go
// Opening database
db, err := ffi.OpenDatabase(path, config)

// Reading values
value, err := revision.Get(key)

// Creating proposals (for writes)
proposal, err := db.Propose(keys, values)

// Committing changes
err := proposal.Commit()

// Iterating (already supported!)
iter, err := revision.Iter()
for iter.Next() {
    key := iter.Key()
    value := iter.Value()
}
iter.Release()
```

---

## Risks & Mitigation

### Risk 1: FFI API Differences
**Risk**: database.Database interface may not map 1:1 to ffi.Database
**Mitigation**: Review both interfaces, add adapter layer if needed
**Likelihood**: Low (both are key-value stores)

### Risk 2: Performance
**Risk**: FFI overhead may impact performance
**Mitigation**: Benchmark against LevelDB, optimize if needed
**Likelihood**: Low (already used in production for EVM)

### Risk 3: Memory Management
**Risk**: CGO memory leaks or crashes
**Mitigation**: Careful iterator lifecycle management, comprehensive tests
**Likelihood**: Low (production-tested in graft/evm/)

---

## Success Criteria

### Phase 3 (Integration) Complete When:
- [ ] database/firewood/db.go uses real ffi.Database
- [ ] All database.Database methods implemented
- [ ] Iterator works (no ErrIteratorNotImplemented)
- [ ] Unit tests pass
- [ ] No compilation errors

### Phase 4 (Testing) Complete When:
- [ ] All unit tests pass
- [ ] Integration tests pass (5 phases)
- [ ] Performance within 20% of LevelDB
- [ ] 24-hour soak test successful

### Phase 5 (Production) Complete When:
- [ ] Deployed to non-critical node
- [ ] 7+ days stable operation
- [ ] State roots match LevelDB
- [ ] Memory usage acceptable

---

## Next Immediate Steps

1. **Read FFI package documentation** - Understand API surface
2. **Update db.go** - Replace placeholders with real implementation
3. **Update iterator.go** - Use ffi iterator functions
4. **Write basic test** - Verify it compiles and runs
5. **Expand tests** - Cover all database operations
6. **Run integration suite** - Execute INTEGRATION_TEST_PLAN.md

---

## Conclusion

We're in an excellent position to proceed:
- ✅ No Rust implementation needed
- ✅ No FFI binding work needed
- ✅ Production-proven code available
- ✅ All dependencies already in go.mod

**Task #3 Progress**: 50% → 60% after this documentation
**Estimated Completion**: 1-2 weeks (was 10 weeks)

Ready to begin integration work immediately.

---

**Author**: Ralph Loop Session, January 24, 2026
**Task**: #3 Firewood Migration (HIGH)
**Phase**: Ready for Phase 3 (Integration)
