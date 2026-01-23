## Firewood Fork Implementation Guide

**Status**: Phase 2 (Weeks 4-5) - Adapter scaffold created, awaiting fork

**Goal**: Add iterator support to Firewood to enable use as AvalancheGo's primary database

---

## Phase 1: Fork Repositories (Week 1-3)

### Step 1.1: Fork on GitHub

1. **Fork Rust Implementation**:
   - Navigate to: https://github.com/ava-labs/firewood
   - Click "Fork" → Create fork under your account
   - Clone: `git clone https://github.com/YOUR-USERNAME/firewood.git`

2. **Fork Go FFI Bindings**:
   - Navigate to: https://github.com/ava-labs/firewood-go-ethhash
   - Click "Fork" → Create fork under your account
   - Clone: `git clone https://github.com/YOUR-USERNAME/firewood-go-ethhash.git`

### Step 1.2: Implement Iterator in Rust

**File**: `firewood/src/db.rs`

Add iterator methods to the Database struct:

```rust
impl Database {
    /// Create iterator over entire database
    pub fn iter(&self) -> DatabaseIterator {
        DatabaseIterator::new(self, None, None)
    }

    /// Create iterator starting from a specific key
    pub fn iter_from(&self, start_key: &[u8]) -> DatabaseIterator {
        DatabaseIterator::new(self, Some(start_key.to_vec()), None)
    }

    /// Create iterator with key prefix filter
    pub fn iter_prefix(&self, prefix: &[u8]) -> DatabaseIterator {
        DatabaseIterator::new(self, None, Some(prefix.to_vec()))
    }
}
```

**File**: `firewood/src/iterator.rs` (NEW)

Create iterator implementation:

```rust
use crate::db::Database;
use std::sync::Arc;

/// Iterator over database key-value pairs
///
/// Traverses the merkle trie in sorted key order.
/// Efficient implementation uses trie structure to avoid
/// loading all keys into memory.
pub struct DatabaseIterator {
    db: Arc<Database>,
    current_key: Option<Vec<u8>>,
    start: Option<Vec<u8>>,
    prefix: Option<Vec<u8>>,
    done: bool,
}

impl DatabaseIterator {
    pub(crate) fn new(
        db: &Database,
        start: Option<Vec<u8>>,
        prefix: Option<Vec<u8>>,
    ) -> Self {
        Self {
            db: Arc::clone(&db.inner),
            current_key: start.clone(),
            start,
            prefix,
            done: false,
        }
    }

    /// Advance to next key-value pair
    ///
    /// Returns true if successful, false if no more items
    pub fn next(&mut self) -> bool {
        if self.done {
            return false;
        }

        // Navigate merkle trie to find next key
        // TODO: Implement trie traversal logic
        // - If current_key is None, start at trie root
        // - Otherwise, find next key in sorted order
        // - Check prefix constraint if set
        // - Update current_key or set done=true

        false // Placeholder
    }

    /// Get current key
    ///
    /// Returns None if iterator is exhausted
    pub fn key(&self) -> Option<&[u8]> {
        self.current_key.as_deref()
    }

    /// Get current value
    ///
    /// Returns None if iterator is exhausted
    pub fn value(&self) -> Option<Vec<u8>> {
        if let Some(ref key) = self.current_key {
            // Retrieve value from database
            self.db.get(key).ok()
        } else {
            None
        }
    }
}

impl Iterator for DatabaseIterator {
    type Item = (Vec<u8>, Vec<u8>);

    fn next(&mut self) -> Option<Self::Item> {
        if self.next() {
            if let Some(ref key) = self.current_key {
                if let Some(value) = self.value() {
                    return Some((key.clone(), value));
                }
            }
        }
        None
    }
}
```

### Step 1.3: Add FFI Bindings

**File**: `firewood-go-ethhash/ffi/iterator.h` (NEW)

C header for FFI:

```c
#ifndef FIREWOOD_ITERATOR_H
#define FIREWOOD_ITERATOR_H

#include <stdint.h>
#include <stdbool.h>

typedef struct DatabaseIterator DatabaseIterator;

// Create iterator over entire database
DatabaseIterator* firewood_db_new_iterator(void* db_handle);

// Create iterator starting from specific key
DatabaseIterator* firewood_db_new_iterator_from(void* db_handle, const uint8_t* start, size_t start_len);

// Create iterator with prefix filter
DatabaseIterator* firewood_db_new_iterator_prefix(void* db_handle, const uint8_t* prefix, size_t prefix_len);

// Advance iterator to next item (returns true if successful)
bool firewood_iterator_next(DatabaseIterator* iter);

// Get current key (returns NULL if exhausted)
const uint8_t* firewood_iterator_key(DatabaseIterator* iter, size_t* out_len);

// Get current value (returns NULL if exhausted)
const uint8_t* firewood_iterator_value(DatabaseIterator* iter, size_t* out_len);

// Free iterator
void firewood_iterator_free(DatabaseIterator* iter);

#endif
```

**File**: `firewood-go-ethhash/ffi/database.go`

Add Go FFI bindings:

```go
// #cgo LDFLAGS: -L${SRCDIR}/../target/release -lfirewood
// #include "iterator.h"
import "C"

import (
    "unsafe"
)

// Iterator wraps Firewood's database iterator
type Iterator struct {
    handle unsafe.Pointer
}

// NewIterator creates an iterator over the entire database
func (db *Database) NewIterator() *Iterator {
    ptr := C.firewood_db_new_iterator(db.handle)
    if ptr == nil {
        return nil
    }
    return &Iterator{handle: unsafe.Pointer(ptr)}
}

// NewIteratorFrom creates an iterator starting from a specific key
func (db *Database) NewIteratorFrom(start []byte) *Iterator {
    var startPtr *C.uint8_t
    if len(start) > 0 {
        startPtr = (*C.uint8_t)(unsafe.Pointer(&start[0]))
    }
    ptr := C.firewood_db_new_iterator_from(db.handle, startPtr, C.size_t(len(start)))
    if ptr == nil {
        return nil
    }
    return &Iterator{handle: unsafe.Pointer(ptr)}
}

// NewIteratorPrefix creates an iterator with a prefix filter
func (db *Database) NewIteratorPrefix(prefix []byte) *Iterator {
    var prefixPtr *C.uint8_t
    if len(prefix) > 0 {
        prefixPtr = (*C.uint8_t)(unsafe.Pointer(&prefix[0]))
    }
    ptr := C.firewood_db_new_iterator_prefix(db.handle, prefixPtr, C.size_t(len(prefix)))
    if ptr == nil {
        return nil
    }
    return &Iterator{handle: unsafe.Pointer(ptr)}
}

// Next advances the iterator to the next key-value pair
// Returns true if successful, false if no more items
func (it *Iterator) Next() bool {
    return bool(C.firewood_iterator_next((*C.DatabaseIterator)(it.handle)))
}

// Key returns the current key
// Returns nil if iterator is exhausted
func (it *Iterator) Key() []byte {
    var len C.size_t
    ptr := C.firewood_iterator_key((*C.DatabaseIterator)(it.handle), &len)
    if ptr == nil {
        return nil
    }
    return C.GoBytes(unsafe.Pointer(ptr), C.int(len))
}

// Value returns the current value
// Returns nil if iterator is exhausted
func (it *Iterator) Value() []byte {
    var len C.size_t
    ptr := C.firewood_iterator_value((*C.DatabaseIterator)(it.handle), &len)
    if ptr == nil {
        return nil
    }
    return C.GoBytes(unsafe.Pointer(ptr), C.int(len))
}

// Release frees the iterator
func (it *Iterator) Release() {
    if it.handle != nil {
        C.firewood_iterator_free((*C.DatabaseIterator)(it.handle))
        it.handle = nil
    }
}
```

### Step 1.4: Test Iterator

**File**: `firewood/tests/iterator_test.rs` (NEW)

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_empty_database_iterator() {
        let db = Database::new_test_db();
        let mut iter = db.iter();
        assert!(!iter.next(), "Empty database should have no items");
    }

    #[test]
    fn test_single_key_iterator() {
        let db = Database::new_test_db();
        db.put(b"key1", b"value1").unwrap();

        let mut iter = db.iter();
        assert!(iter.next(), "Should find one item");
        assert_eq!(iter.key().unwrap(), b"key1");
        assert_eq!(iter.value().unwrap(), b"value1");
        assert!(!iter.next(), "Should have no more items");
    }

    #[test]
    fn test_multiple_keys_sorted() {
        let db = Database::new_test_db();
        db.put(b"key3", b"value3").unwrap();
        db.put(b"key1", b"value1").unwrap();
        db.put(b"key2", b"value2").unwrap();

        let mut iter = db.iter();
        let keys: Vec<_> = std::iter::from_fn(|| {
            if iter.next() {
                Some(iter.key().unwrap().to_vec())
            } else {
                None
            }
        }).collect();

        assert_eq!(keys, vec![b"key1", b"key2", b"key3"]);
    }

    #[test]
    fn test_iterator_from_start() {
        let db = Database::new_test_db();
        db.put(b"key1", b"value1").unwrap();
        db.put(b"key2", b"value2").unwrap();
        db.put(b"key3", b"value3").unwrap();

        let mut iter = db.iter_from(b"key2");
        assert!(iter.next());
        assert_eq!(iter.key().unwrap(), b"key2");
        assert!(iter.next());
        assert_eq!(iter.key().unwrap(), b"key3");
        assert!(!iter.next());
    }

    #[test]
    fn test_iterator_prefix() {
        let db = Database::new_test_db();
        db.put(b"prefix:key1", b"value1").unwrap();
        db.put(b"prefix:key2", b"value2").unwrap();
        db.put(b"other:key3", b"value3").unwrap();

        let mut iter = db.iter_prefix(b"prefix:");
        let mut count = 0;
        while iter.next() {
            assert!(iter.key().unwrap().starts_with(b"prefix:"));
            count += 1;
        }
        assert_eq!(count, 2);
    }
}
```

---

## Phase 2: Update AvalancheGo Adapter (Current - Weeks 4-5)

### Step 2.1: Update db.go

Replace placeholder implementations in `database/firewood/db.go`:

1. Uncomment FFI imports:
   ```go
   import (
       "github.com/YOUR-USERNAME/firewood-go-ethhash/ffi"
   )
   ```

2. Implement all TODOs using FFI bindings
3. Add proper error handling
4. Implement iterator wrapper

**File**: `database/firewood/iterator.go` (NEW)

```go
package firewood

import (
    "github.com/ava-labs/avalanchego/database"
    // TODO: Use forked version
    // "github.com/YOUR-USERNAME/firewood-go-ethhash/ffi"
)

type iterator struct {
    // fw  *ffi.Iterator
    // log logging.Logger
    // err error
}

func (it *iterator) Next() bool {
    // return it.fw.Next()
    return false
}

func (it *iterator) Error() error {
    // return it.err
    return ErrIteratorNotImplemented
}

func (it *iterator) Key() []byte {
    // return it.fw.Key()
    return nil
}

func (it *iterator) Value() []byte {
    // return it.fw.Value()
    return nil
}

func (it *iterator) Release() {
    // it.fw.Release()
}
```

### Step 2.2: Implement Batch Operations

Complete batch implementation with buffering and atomic writes.

---

## Phase 3: Factory Integration (Week 6)

### Step 3.1: Update go.mod

**File**: `go.mod`

Add replace directives:

```go
replace (
    github.com/ava-labs/firewood => github.com/YOUR-USERNAME/firewood v0.1.0
    github.com/ava-labs/firewood-go-ethhash => github.com/YOUR-USERNAME/firewood-go-ethhash v0.1.0
)
```

### Step 3.2: Update Factory

**File**: `database/factory/factory.go`

Add Firewood case:

```go
import (
    "github.com/ava-labs/avalanchego/database/firewood"
)

func New(name string, ...) (database.Database, error) {
    switch name {
    case leveldb.Name:
        return leveldb.New(...)
    case pebble.Name:
        return pebble.New(...)
    case firewood.Name:  // NEW
        return firewood.New(dir, configBytes, log)
    default:
        return nil, fmt.Errorf("unknown database type: %s", name)
    }
}
```

---

## Testing Strategy

### Unit Tests
- Iterator functionality (Next, Key, Value, Release)
- Iterator edge cases (empty DB, single key, prefix matching)
- Memory leak detection with CGO

### Integration Tests
1. Bootstrap P-chain with 1K blocks using Firewood
2. Bootstrap C-chain with 100K blocks
3. Compare state roots with LevelDB (must match exactly)
4. 24-hour soak test with memory monitoring

### Performance Benchmarks
```bash
go test -bench=. ./database/firewood/... -benchmem
# Compare vs LevelDB/PebbleDB
```

---

## Timeline

- **Week 1-3**: Fork repos, implement iterator, test
- **Week 4-5**: Complete adapter implementation (current)
- **Week 6**: Factory integration, go.mod updates
- **Week 7**: Migration tool, node integration
- **Week 8-10**: Testing, performance validation, upstream contribution

---

## Success Criteria

- ✅ All database interface tests pass
- ✅ Iterator works correctly in all cases
- ✅ State roots match LevelDB exactly
- ✅ Performance within 20% of LevelDB
- ✅ Memory stable over 24+ hours
- ✅ No CGO memory leaks

---

## Next Steps

1. Create GitHub forks
2. Implement Rust iterator (follow spec above)
3. Add FFI bindings
4. Test iterator thoroughly
5. Update adapter to use forked version
6. Integration testing

---

*Generated*: Week 5 (January 2026)
*Status*: Phase 2 scaffold complete, awaiting fork implementation
