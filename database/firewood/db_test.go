// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/database"
)

// TestDatabaseInterface verifies that Database implements database.Database
func TestDatabaseInterface(t *testing.T) {
	var _ database.Database = &Database{}
}

// TestNew_Placeholder verifies constructor returns without panic
// TODO: Expand once fork is ready
func TestNew_Placeholder(t *testing.T) {
	db, err := New("/tmp/test-firewood", []byte("{}"), nil)
	require.NoError(t, err)
	require.NotNil(t, db)

	err = db.Close()
	require.NoError(t, err)
}

// TestIteratorNotImplemented verifies placeholder returns expected error
// TODO: Remove once fork is ready and replace with real iterator tests
func TestIteratorNotImplemented(t *testing.T) {
	db, err := New("/tmp/test-firewood", []byte("{}"), nil)
	require.NoError(t, err)
	defer db.Close()

	// All operations should return ErrIteratorNotImplemented
	_, err = db.Has([]byte("key"))
	require.ErrorIs(t, err, ErrIteratorNotImplemented)

	_, err = db.Get([]byte("key"))
	require.ErrorIs(t, err, ErrIteratorNotImplemented)

	err = db.Put([]byte("key"), []byte("value"))
	require.ErrorIs(t, err, ErrIteratorNotImplemented)

	err = db.Delete([]byte("key"))
	require.ErrorIs(t, err, ErrIteratorNotImplemented)

	// Iterator methods should return error iterator
	iter := db.NewIterator()
	require.NotNil(t, iter)
	require.False(t, iter.Next())
	require.ErrorIs(t, iter.Error(), ErrIteratorNotImplemented)
}

// TODO: Add comprehensive tests once fork is ready
//
// Tests to implement:
// - TestBasicOperations: Put, Get, Has, Delete
// - TestBatchOperations: Batch Put/Delete, Write, Reset, Replay
// - TestIterator: NewIterator, NewIteratorWithStart, NewIteratorWithPrefix
// - TestIteratorOrdering: Verify keys returned in sorted order
// - TestIteratorPrefix: Verify prefix filtering works correctly
// - TestCompact: Verify compaction (if supported by Firewood)
// - TestHealthCheck: Verify health check returns correct status
// - TestConcurrency: Multiple goroutines accessing database
// - TestMemoryLeaks: Run with -race, verify no leaks
//
// Integration tests (separate file):
// - TestBootstrap: Bootstrap P-chain using Firewood
// - TestStateRoots: Compare state roots with LevelDB
// - TestPerformance: Benchmark vs LevelDB/PebbleDB
// - TestSoakTest: 24-hour stability test

// Benchmark placeholders - to be implemented with fork

// BenchmarkPut benchmarks Put operations
// TODO: Implement once fork is ready
// func BenchmarkPut(b *testing.B) {
//     db, err := New("/tmp/bench-firewood", []byte("{}"), nil)
//     require.NoError(b, err)
//     defer db.Close()
//
//     key := make([]byte, 32)
//     value := make([]byte, 256)
//
//     b.ResetTimer()
//     for i := 0; i < b.N; i++ {
//         binary.BigEndian.PutUint64(key, uint64(i))
//         err := db.Put(key, value)
//         require.NoError(b, err)
//     }
// }

// BenchmarkGet benchmarks Get operations
// TODO: Implement once fork is ready

// BenchmarkIterator benchmarks iterator traversal
// TODO: Implement once fork is ready

// BenchmarkBatch benchmarks batch operations
// TODO: Implement once fork is ready
