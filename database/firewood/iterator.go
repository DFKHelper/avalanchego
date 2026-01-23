// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/logging"

	// TODO: Uncomment once fork is ready with iterator support
	// "github.com/YOUR-FORK/firewood-go-ethhash/ffi"
)

// iterator implements database.Iterator for Firewood
//
// Wraps the FFI iterator from forked firewood-go-ethhash.
// Provides AvalancheGo's Iterator interface over Firewood's native iterator.
type iterator struct {
	// TODO: Uncomment once fork is ready
	// fw   *ffi.Iterator
	// log  logging.Logger
	// err  error
	// released bool

	log logging.Logger
	err error
}

// newIterator creates a new iterator wrapper
// TODO: Implement once fork is ready
// func newIterator(fw *ffi.Iterator, log logging.Logger) *iterator {
//     return &iterator{
//         fw:  fw,
//         log: log,
//     }
// }

// Next implements database.Iterator
// Advances the iterator to the next key-value pair.
// Returns true if the iterator is pointing at a valid entry and false if not.
func (it *iterator) Next() bool {
	// TODO: Implement once fork is ready
	// if it.released {
	//     it.err = ErrClosed
	//     return false
	// }
	//
	// hasNext := it.fw.Next()
	// if !hasNext {
	//     // Iterator exhausted or error occurred
	//     // Check if there was an FFI error
	//     if ffiErr := it.fw.Error(); ffiErr != nil {
	//         it.err = ffiErr
	//     }
	// }
	// return hasNext

	it.err = ErrIteratorNotImplemented
	return false
}

// Error implements database.Iterator
// Returns any accumulated error. Exhausting all the key/value pairs
// is not considered to be an error.
func (it *iterator) Error() error {
	// TODO: Implement once fork is ready
	// return it.err
	return ErrIteratorNotImplemented
}

// Key implements database.Iterator
// Returns the key of the current key/value pair, or nil if done.
// The caller should not modify the contents of the returned slice, and
// its contents may change on the next call to Next.
func (it *iterator) Key() []byte {
	// TODO: Implement once fork is ready
	// if it.released || it.err != nil {
	//     return nil
	// }
	// return it.fw.Key()
	return nil
}

// Value implements database.Iterator
// Returns the value of the current key/value pair, or nil if done.
// The caller should not modify the contents of the returned slice, and
// its contents may change on the next call to Next.
func (it *iterator) Value() []byte {
	// TODO: Implement once fork is ready
	// if it.released || it.err != nil {
	//     return nil
	// }
	// return it.fw.Value()
	return nil
}

// Release implements database.Iterator
// Releases associated resources. Release should always succeed and can
// be called multiple times without causing error.
func (it *iterator) Release() {
	// TODO: Implement once fork is ready
	// if !it.released {
	//     it.fw.Release()
	//     it.released = true
	// }
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
