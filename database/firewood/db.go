// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"context"
	"errors"
	"fmt"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/logging"

	// TODO: Replace with forked version once iterator support is added
	// "github.com/YOUR-FORK/firewood-go-ethhash/ffi"
)

const (
	// Name is the name of this database for database switches
	Name = "firewood"
)

var (
	ErrIteratorNotImplemented = errors.New("iterator support pending firewood fork implementation")
	ErrClosed                 = errors.New("database closed")
)

// Database implements the database.Database interface using Firewood.
//
// Current Status: PLACEHOLDER - Awaiting Firewood fork with iterator support
//
// Implementation Plan:
// 1. Fork github.com/ava-labs/firewood (Rust implementation)
// 2. Fork github.com/ava-labs/firewood-go-ethhash (Go FFI bindings)
// 3. Implement iterator in Rust:
//    - Add db.iter(), db.iter_from(), db.iter_prefix() to firewood/src/db.rs
//    - Create firewood/src/iterator.rs with DatabaseIterator struct
// 4. Add FFI bindings for iterator in firewood-go-ethhash/ffi/database.go
// 5. Update this file to use forked version with iterator support
//
// Timeline: Weeks 4-5 (Phase 2: AvalancheGo Adapter)
type Database struct {
	// TODO: Uncomment once fork is ready
	// fw    *ffi.Database
	// log   logging.Logger
	// ctx   context.Context
	// closed atomic.Bool

	log logging.Logger
}

// New creates a new Firewood database instance.
//
// Parameters:
//   - file: Path to database directory
//   - configBytes: JSON-encoded Config (see config.go)
//   - log: Logger instance
//
// Returns database.Database implementation or error if initialization fails.
func New(file string, configBytes []byte, log logging.Logger) (database.Database, error) {
	log.Warn("Firewood database adapter is a placeholder - awaiting iterator implementation in fork")

	// TODO: Parse config from configBytes
	// var cfg Config
	// if err := json.Unmarshal(configBytes, &cfg); err != nil {
	//     return nil, fmt.Errorf("failed to parse config: %w", err)
	// }

	// TODO: Initialize Firewood database from fork
	// fw, err := ffi.OpenDatabase(file, cfg.toFFIConfig())
	// if err != nil {
	//     return nil, fmt.Errorf("failed to open firewood database: %w", err)
	// }

	return &Database{
		log: log,
	}, nil
}

// Has implements database.KeyValueReader
func (db *Database) Has(key []byte) (bool, error) {
	// TODO: Implement using forked Firewood
	// if db.closed.Load() {
	//     return false, ErrClosed
	// }
	// return db.fw.Has(key)
	return false, ErrIteratorNotImplemented
}

// Get implements database.KeyValueReader
func (db *Database) Get(key []byte) ([]byte, error) {
	// TODO: Implement using forked Firewood
	// if db.closed.Load() {
	//     return nil, ErrClosed
	// }
	// value, err := db.fw.Get(key)
	// if err != nil {
	//     if errors.Is(err, ffi.ErrNotFound) {
	//         return nil, database.ErrNotFound
	//     }
	//     return nil, err
	// }
	// return value, nil
	return nil, ErrIteratorNotImplemented
}

// Put implements database.KeyValueWriter
func (db *Database) Put(key []byte, value []byte) error {
	// TODO: Implement using forked Firewood
	// if db.closed.Load() {
	//     return ErrClosed
	// }
	// return db.fw.Put(key, value)
	return ErrIteratorNotImplemented
}

// Delete implements database.KeyValueDeleter
func (db *Database) Delete(key []byte) error {
	// TODO: Implement using forked Firewood
	// if db.closed.Load() {
	//     return ErrClosed
	// }
	// return db.fw.Delete(key)
	return ErrIteratorNotImplemented
}

// NewBatch implements database.Batcher
func (db *Database) NewBatch() database.Batch {
	// TODO: Implement batch operations
	// return &batch{
	//     db:   db,
	//     ops:  make([]database.BatchOp, 0, 256),
	// }
	return &batch{db: db}
}

// NewIterator implements database.Iteratee
// CRITICAL: This requires iterator support in Firewood fork
func (db *Database) NewIterator() database.Iterator {
	// TODO: Implement once fork has iterator support
	// if db.closed.Load() {
	//     return newErrorIterator(ErrClosed)
	// }
	// fwIter := db.fw.NewIterator()
	// return &iterator{
	//     fw:  fwIter,
	//     log: db.log,
	// }
	return newErrorIterator(ErrIteratorNotImplemented)
}

// NewIteratorWithStart implements database.Iteratee
func (db *Database) NewIteratorWithStart(start []byte) database.Iterator {
	// TODO: Implement once fork has iterator support
	// if db.closed.Load() {
	//     return newErrorIterator(ErrClosed)
	// }
	// fwIter := db.fw.NewIteratorFrom(start)
	// return &iterator{
	//     fw:  fwIter,
	//     log: db.log,
	// }
	return newErrorIterator(ErrIteratorNotImplemented)
}

// NewIteratorWithPrefix implements database.Iteratee
func (db *Database) NewIteratorWithPrefix(prefix []byte) database.Iterator {
	// TODO: Implement once fork has iterator support
	// if db.closed.Load() {
	//     return newErrorIterator(ErrClosed)
	// }
	// fwIter := db.fw.NewIteratorPrefix(prefix)
	// return &iterator{
	//     fw:  fwIter,
	//     log: db.log,
	// }
	return newErrorIterator(ErrIteratorNotImplemented)
}

// NewIteratorWithStartAndPrefix implements database.Iteratee
func (db *Database) NewIteratorWithStartAndPrefix(start, prefix []byte) database.Iterator {
	// TODO: Implement once fork has iterator support
	// Could be implemented as:
	// 1. NewIteratorPrefix(prefix)
	// 2. Seek to start
	// 3. Validate key still has prefix
	return newErrorIterator(ErrIteratorNotImplemented)
}

// Compact implements database.Compacter
func (db *Database) Compact(start []byte, limit []byte) error {
	// Firewood is a merkle trie database - compaction may not be applicable
	// or could trigger internal optimization routines if available
	// TODO: Check if Firewood has compaction support
	return nil
}

// Close implements io.Closer
func (db *Database) Close() error {
	// TODO: Implement cleanup
	// if !db.closed.CompareAndSwap(false, true) {
	//     return ErrClosed
	// }
	// return db.fw.Close()
	return nil
}

// HealthCheck implements health.Checker
func (db *Database) HealthCheck(ctx context.Context) (interface{}, error) {
	// TODO: Implement health check
	// Basic check: verify database is not closed and can perform a read
	// if db.closed.Load() {
	//     return nil, ErrClosed
	// }
	//
	// // Try a simple operation to verify database is responsive
	// _, err := db.Has([]byte("health-check"))
	// if err != nil {
	//     return nil, fmt.Errorf("health check failed: %w", err)
	// }
	//
	// return map[string]interface{}{
	//     "database": "firewood",
	//     "status":   "healthy",
	// }, nil

	return nil, ErrIteratorNotImplemented
}

// batch implements database.Batch for Firewood
type batch struct {
	db  *Database
	ops []database.BatchOp
}

func (b *batch) Put(key []byte, value []byte) error {
	// TODO: Buffer operation
	// b.ops = append(b.ops, database.BatchOp{
	//     Key:    append([]byte(nil), key...),
	//     Value:  append([]byte(nil), value...),
	//     Delete: false,
	// })
	// return nil
	return ErrIteratorNotImplemented
}

func (b *batch) Delete(key []byte) error {
	// TODO: Buffer operation
	// b.ops = append(b.ops, database.BatchOp{
	//     Key:    append([]byte(nil), key...),
	//     Delete: true,
	// })
	// return nil
	return ErrIteratorNotImplemented
}

func (b *batch) ValueSize() int {
	// TODO: Calculate total size of buffered operations
	// total := 0
	// for _, op := range b.ops {
	//     total += len(op.Key) + len(op.Value)
	// }
	// return total
	return 0
}

func (b *batch) Write() error {
	// TODO: Execute all buffered operations atomically
	// for _, op := range b.ops {
	//     if op.Delete {
	//         if err := b.db.Delete(op.Key); err != nil {
	//             return err
	//         }
	//     } else {
	//         if err := b.db.Put(op.Key, op.Value); err != nil {
	//             return err
	//         }
	//     }
	// }
	// return nil
	return ErrIteratorNotImplemented
}

func (b *batch) Reset() {
	// TODO: Clear buffered operations
	// b.ops = b.ops[:0]
}

func (b *batch) Replay(w database.KeyValueWriterDeleter) error {
	// TODO: Replay operations to another database
	// for _, op := range b.ops {
	//     if op.Delete {
	//         if err := w.Delete(op.Key); err != nil {
	//             return err
	//         }
	//     } else {
	//         if err := w.Put(op.Key, op.Value); err != nil {
	//             return err
	//         }
	//     }
	// }
	// return nil
	return ErrIteratorNotImplemented
}

func (b *batch) Inner() database.Batch {
	return b
}

// errorIterator is a placeholder iterator that returns an error
type errorIterator struct {
	err error
}

func newErrorIterator(err error) database.Iterator {
	return &errorIterator{err: err}
}

func (it *errorIterator) Next() bool { return false }
func (it *errorIterator) Error() error { return it.err }
func (it *errorIterator) Key() []byte { return nil }
func (it *errorIterator) Value() []byte { return nil }
func (it *errorIterator) Release() {}
