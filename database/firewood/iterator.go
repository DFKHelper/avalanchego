// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"bytes"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/firewood-go-ethhash/ffi"
)

// iterator implements database.Iterator for Firewood
//
// Wraps the FFI iterator from firewood-go-ethhash.
// Provides AvalancheGo's Iterator interface over Firewood's native iterator.
//
// Note: This is a merge iterator that combines:
// 1. Committed state from Firewood (via ffi.Iterator)
// 2. Pending writes not yet flushed (from Database.pending)
type iterator struct {
	fw       *ffi.Iterator      // Firewood FFI iterator
	pending  []pendingKV        // Pending operations from batch, sorted by key
	pendIdx  int                // Current index in pending slice
	log      logging.Logger
	err      error
	released bool

	// Current state
	fwValid      bool   // Whether fw iterator has current value
	pendingValid bool   // Whether pending has current value
	currentKey   []byte // Current key (from whichever source is smaller)
	currentValue []byte // Current value
}

type pendingKV struct {
	key    []byte
	value  []byte
	delete bool
}

// newIterator creates a new merge iterator
// pending operations are passed in and sorted for merge iteration
func newIterator(fw *ffi.Iterator, pending []pendingKV, log logging.Logger) *iterator {
	it := &iterator{
		fw:      fw,
		pending: pending,
		pendIdx: 0,
		log:     log,
	}

	// Advance both iterators to first position
	it.fwValid = fw.Next()
	it.pendingValid = it.pendIdx < len(it.pending)

	return it
}

// Next implements database.Iterator
// Advances the iterator to the next key-value pair.
// Returns true if the iterator is pointing at a valid entry and false if not.
func (it *iterator) Next() bool {
	if it.released {
		it.err = ErrClosed
		return false
	}

	// Check if either source has data
	if !it.fwValid && !it.pendingValid {
		return false
	}

	// Merge logic: pick the smallest key from fw and pending
	var useFirewood bool

	if !it.fwValid {
		// Only pending has data
		useFirewood = false
	} else if !it.pendingValid {
		// Only firewood has data
		useFirewood = true
	} else {
		// Both have data - compare keys
		cmp := bytes.Compare(it.fw.Key(), it.pending[it.pendIdx].key)
		if cmp < 0 {
			// Firewood key is smaller
			useFirewood = true
		} else if cmp > 0 {
			// Pending key is smaller
			useFirewood = false
		} else {
			// Same key - pending overrides committed state
			useFirewood = false
			// Advance firewood past this duplicate key
			it.fwValid = it.fw.Next()
		}
	}

	if useFirewood {
		// Use firewood value
		it.currentKey = it.fw.Key()
		it.currentValue = it.fw.Value()
		it.fwValid = it.fw.Next()
		return true
	} else {
		// Use pending value
		op := it.pending[it.pendIdx]
		it.currentKey = op.key
		if op.delete {
			// Deleted key - skip to next
			it.pendIdx++
			it.pendingValid = it.pendIdx < len(it.pending)
			return it.Next() // Recursively get next non-deleted entry
		}
		it.currentValue = op.value
		it.pendIdx++
		it.pendingValid = it.pendIdx < len(it.pending)
		return true
	}
}

// Error implements database.Iterator
// Returns any accumulated error. Exhausting all the key/value pairs
// is not considered to be an error.
func (it *iterator) Error() error {
	if it.err != nil {
		return it.err
	}
	// Check FFI iterator error
	return it.fw.Err()
}

// Key implements database.Iterator
// Returns the key of the current key/value pair, or nil if done.
// The caller should not modify the contents of the returned slice, and
// its contents may change on the next call to Next.
func (it *iterator) Key() []byte {
	if it.released || it.err != nil {
		return nil
	}
	return it.currentKey
}

// Value implements database.Iterator
// Returns the value of the current key/value pair, or nil if done.
// The caller should not modify the contents of the returned slice, and
// its contents may change on the next call to Next.
func (it *iterator) Value() []byte {
	if it.released || it.err != nil {
		return nil
	}
	return it.currentValue
}

// Release implements database.Iterator
// Releases associated resources. Release should always succeed and can
// be called multiple times without causing error.
func (it *iterator) Release() {
	if !it.released {
		it.fw.Drop()
		it.released = true
	}
}

// errorIterator is a special iterator that always returns an error
// Used for returning iterators when the database is closed or operations fail
type errorIterator struct {
	err error
}

// newErrorIterator creates an iterator that always returns the given error
func newErrorIterator(err error) database.Iterator {
	return &errorIterator{err: err}
}

func (it *errorIterator) Next() bool       { return false }
func (it *errorIterator) Error() error     { return it.err }
func (it *errorIterator) Key() []byte      { return nil }
func (it *errorIterator) Value() []byte    { return nil }
func (it *errorIterator) Release()         {}
