// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"bytes"
	"encoding/hex"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/firewood-go-ethhash/ffi"
	"go.uber.org/zap"
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
	fw       *ffi.Iterator // Firewood FFI iterator
	pending  []pendingKV   // Pending operations from batch, sorted by key
	pendIdx  int           // Current index in pending slice
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
		it.err = database.ErrClosed
		return false
	}

	// PHASE 3 FIX: Use iteration instead of recursion to prevent stack overflow
	// When Firewood returns many invalid keys in a row, recursive Next() calls
	// can overflow the stack. Loop instead.
	const maxSkippedKeys = 100000 // Safety limit to prevent infinite loops
	skippedKeys := 0
	validKeysReturned := 0

	for {
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
			// DEFENSIVE: Check FFI iterator health before calling methods
			// If FFI is panicking, we want to catch patterns early
			if it.fw == nil {
				if it.log != nil {
					it.log.Error("Firewood iterator is nil - critical FFI error",
						zap.Int("skippedKeys", skippedKeys),
						zap.Int("validKeysReturned", validKeysReturned),
					)
				}
				it.err = database.ErrClosed
				return false
			}

			// Get raw data from Firewood
			// Wrap in defer to catch potential panics from FFI
			var rawKey, rawValue []byte
			func() {
				defer func() {
					if r := recover(); r != nil {
						if it.log != nil {
							it.log.Error("RECOVERED PANIC in FFI Key() call",
								zap.Any("panic", r),
								zap.Int("skippedKeys", skippedKeys),
								zap.Int("validKeysReturned", validKeysReturned),
							)
						}
					}
				}()
				rawKey = it.fw.Key()
				rawValue = it.fw.Value()
			}()

			// Check for obviously corrupted/invalid data from FFI
			if len(rawKey) == 0 {
				if it.log != nil {
					it.log.Warn("FFI returned empty key - possible iterator corruption",
						zap.Int("skippedKeys", skippedKeys),
						zap.Int("validKeysReturned", validKeysReturned),
					)
				}
				// Try to advance anyway
				it.fwValid = it.fw.Next()
				continue
			}

			// Safety check: if key is unreasonably large, skip it
			if len(rawKey) > 10000 {
				if it.log != nil {
					it.log.Warn("FFI returned extremely large key - likely corruption",
						zap.Int("keyLen", len(rawKey)),
						zap.Int("skippedKeys", skippedKeys),
						zap.Int("validKeysReturned", validKeysReturned),
					)
				}
				it.fwValid = it.fw.Next()
				skippedKeys++
				continue
			}

			// DEBUG: Log what Firewood iterator returns before transformation (sample only)
			if it.log != nil && validKeysReturned < 10 {
				keyLen := len(rawKey)
				valueLen := len(rawValue)
				keyHex := hex.EncodeToString(rawKey[:min(keyLen, 32)])
				valueHex := hex.EncodeToString(rawValue[:min(valueLen, 32)])
				keyASCII := isASCII(rawKey[:min(keyLen, 32)])
				valueASCII := isASCII(rawValue[:min(valueLen, 32)])

				it.log.Debug("Firewood Iterator.Next() raw data BEFORE transform",
					zap.Int("keyLen", keyLen),
					zap.Int("valueLen", valueLen),
					zap.String("keyHex", keyHex),
					zap.String("valueHex", valueHex),
					zap.Bool("keyLooksASCII", keyASCII),
					zap.Bool("valueLooksASCII", valueASCII),
					zap.Int("validKeysReturned", validKeysReturned),
				)
			}

			// PHASE 3 FIX: Transform iterator keys and validate
			// Firewood iterator returns internal trie nodes (97-129 bytes) mixed with leaf nodes
			// We must extract and validate the key, skipping invalid internal nodes
			transformedKey, valid := transformAndValidateIteratorKey(rawKey, it.log)

			// Advance Firewood iterator
			func() {
				defer func() {
					if r := recover(); r != nil {
						if it.log != nil {
							it.log.Error("RECOVERED PANIC in FFI Next() call",
								zap.Any("panic", r),
								zap.Int("skippedKeys", skippedKeys),
								zap.Int("validKeysReturned", validKeysReturned),
							)
						}
					}
				}()
				it.fwValid = it.fw.Next()
			}()

			if !valid {
				// This was an internal trie node or invalid key - skip it
				// Continue loop to get the next valid key
				skippedKeys++
				if skippedKeys%10000 == 0 && it.log != nil {
					it.log.Info("Iterator progress: processing trie nodes",
						zap.Int("skippedInvalidKeys", skippedKeys),
						zap.Int("validKeysReturned", validKeysReturned),
					)
				}

				if skippedKeys >= maxSkippedKeys {
					if it.log != nil {
						it.log.Warn("Exceeded max skipped keys - iterator may be returning only invalid keys",
							zap.Int("maxSkippedKeys", maxSkippedKeys),
						)
					}
					return false
				}

				continue // Skip to next iteration
			}

			// Valid application key found
			it.currentKey = transformedKey
			it.currentValue = rawValue
			validKeysReturned++

			// DEBUG: Log transformed key (first few only)
			if it.log != nil && validKeysReturned <= 10 {
				it.log.Debug("Firewood Iterator.Next() key AFTER transform (VALID)",
					zap.Int("originalKeyLen", len(rawKey)),
					zap.Int("transformedKeyLen", len(it.currentKey)),
					zap.String("transformedKeyHex", hex.EncodeToString(it.currentKey)),
					zap.Int("totalSkipped", skippedKeys),
					zap.Int("validKeysTotal", validKeysReturned),
				)
			}

			// Milestone logging every 100 valid keys
			if validKeysReturned%100 == 0 && it.log != nil {
				it.log.Info("Iterator milestone: valid keys returned",
					zap.Int("validKeys", validKeysReturned),
					zap.Int("skippedInvalidKeys", skippedKeys),
				)
			}

			return true
			} else {
			// Use pending value
			op := it.pending[it.pendIdx]
			it.currentKey = op.key
			if op.delete {
				// Deleted key - skip to next
				it.pendIdx++
				it.pendingValid = it.pendIdx < len(it.pending)
				continue // Loop to get next non-deleted entry
			}
			it.currentValue = op.value
			it.pendIdx++
			it.pendingValid = it.pendIdx < len(it.pending)
			return true
		}
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

func (it *errorIterator) Next() bool    { return false }
func (it *errorIterator) Error() error  { return it.err }
func (it *errorIterator) Key() []byte   { return nil }
func (it *errorIterator) Value() []byte { return nil }
func (it *errorIterator) Release()      {}
