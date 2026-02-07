// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"bytes"
	"sort"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/logging"
	"go.uber.org/zap"
)

// pendingKV represents a key-value operation (put or delete) in pending batch
type pendingKV struct {
	key    []byte
	value  []byte
	delete bool
}

// iterator implements database.Iterator for Firewood
//
// Architecture: Registry-based iterator
// - Firewood's rev.Iter() returns merkle nodes (97-129 bytes), not actual keys
// - Instead, we maintain a registry of all committed keys
// - Iterator uses this registry to iterate over actual keys
// - Values are fetched from Firewood database on demand
//
// The iterator merges:
// 1. Registered committed keys from database registry
// 2. Pending operations not yet flushed (from Database.pending)
type iterator struct {
	// Sorted list of all keys to iterate
	// Combined from: registered keys + pending operations (non-deleted)
	allKeys [][]byte
	keyIdx  int // Current index in allKeys

	// Database reference for fetching values
	db  *Database
	log logging.Logger

	// Current state
	currentKey   []byte // Current key
	currentValue []byte // Current value
	err          error
	released     bool

	// Optional filters
	startKey []byte // Only iterate keys >= startKey
	prefix   []byte // Only iterate keys with this prefix
}

// newIterator creates a new registry-based iterator
// It combines committed keys from the registry with pending operations
func newIterator(
	db *Database,
	pending []pendingKV,
	startKey []byte,
	prefix []byte,
	log logging.Logger,
) *iterator {
	// Collect all unique keys from registry + pending
	keySet := make(map[string]bool)
	allKeys := make([][]byte, 0)

	// Add registered keys from database
	db.registryMu.RLock()
	for keyStr := range db.registry {
		key := []byte(keyStr)
		keySet[keyStr] = true
		allKeys = append(allKeys, key)
	}
	db.registryMu.RUnlock()

	// Add pending keys (overriding registry if present)
	for _, op := range pending {
		keyStr := string(op.key)
		if op.delete {
			// Delete operation: remove from set
			delete(keySet, keyStr)
			// We don't add deleted keys to allKeys
		} else {
			// Put operation: add if not already present
			if !keySet[keyStr] {
				allKeys = append(allKeys, op.key)
			}
		}
	}

	// Sort keys for consistent iteration order
	sort.Slice(allKeys, func(i, j int) bool {
		return bytes.Compare(allKeys[i], allKeys[j]) < 0
	})

	// Filter by startKey and prefix if specified
	filtered := make([][]byte, 0, len(allKeys))
	for _, key := range allKeys {
		// Skip keys before startKey if specified
		if len(startKey) > 0 && bytes.Compare(key, startKey) < 0 {
			continue
		}

		// Skip keys without prefix if specified
		if len(prefix) > 0 && !bytes.HasPrefix(key, prefix) {
			continue
		}

		filtered = append(filtered, key)
	}

	if log != nil {
		log.Debug("Created new registry-based iterator",
			zap.Int("totalKeys", len(filtered)),
			zap.Int("pendingOps", len(pending)),
			zap.Bool("hasStartKey", len(startKey) > 0),
			zap.Bool("hasPrefix", len(prefix) > 0),
		)
	}

	return &iterator{
		allKeys:  filtered,
		keyIdx:   -1, // Start before first key
		db:       db,
		log:      log,
		startKey: startKey,
		prefix:   prefix,
	}
}

// Next implements database.Iterator
// Advances to the next key-value pair.
// Returns true if valid, false if done or error.
func (it *iterator) Next() bool {
	if it.released {
		it.err = database.ErrClosed
		return false
	}

	// Advance to next key
	it.keyIdx++

	// Check if we've exhausted all keys
	if it.keyIdx >= len(it.allKeys) {
		return false
	}

	// Get the current key
	key := it.allKeys[it.keyIdx]
	it.currentKey = key

	// Fetch value from database (checks pending first, then Firewood)
	value, err := it.db.Get(key)
	if err != nil {
		if err == database.ErrNotFound {
			// Key was deleted, skip it and continue
			if it.log != nil {
				it.log.Debug("Iterator: key not found (likely deleted)",
					zap.Int("keyIndex", it.keyIdx),
				)
			}
			return it.Next() // Skip and get next
		}
		// Real error
		it.err = err
		return false
	}

	it.currentValue = value
	return true
}

// Error implements database.Iterator
// Returns any accumulated error.
func (it *iterator) Error() error {
	return it.err
}

// Key implements database.Iterator
// Returns the key of the current pair, or nil if done.
func (it *iterator) Key() []byte {
	if it.released || it.err != nil {
		return nil
	}
	return it.currentKey
}

// Value implements database.Iterator
// Returns the value of the current pair, or nil if done.
func (it *iterator) Value() []byte {
	if it.released || it.err != nil {
		return nil
	}
	return it.currentValue
}

// Release implements database.Iterator
// Releases associated resources.
func (it *iterator) Release() {
	it.released = true
	it.allKeys = nil
	it.currentKey = nil
	it.currentValue = nil
}

// errorIterator is a special iterator that always returns an error
// Used when database is closed or operations fail
type errorIterator struct {
	err error
}

// newErrorIterator creates an iterator that always returns the given error
func newErrorIterator(err error) database.Iterator {
	return &errorIterator{err: err}
}

func (it *errorIterator) Next() bool    { return false }
func (it *errorIterator) Error() error  { return it.err }
func (it *errorIterator) Key() []byte   { return nil }
func (it *errorIterator) Value() []byte { return nil }
func (it *errorIterator) Release()      {}
