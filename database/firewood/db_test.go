// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build !windows
// +build !windows

package firewood

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/logging"
)

// TestFirewoodBasicOperations tests basic Put/Get/Delete operations
func TestFirewoodBasicOperations(t *testing.T) {
	require := require.New(t)

	tmpDir, err := os.MkdirTemp("", "firewood-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	log := logging.NoLog{}
	db, err := New(dbPath, nil, log)
	require.NoError(err)
	require.NotNil(db)
	defer db.Close()

	// Test Put
	key := []byte("test-key")
	value := []byte("test-value")
	err = db.Put(key, value)
	require.NoError(err)

	// Test Has (pending batch)
	has, err := db.Has(key)
	require.NoError(err)
	require.True(has, "should find key in pending batch")

	// Test Get (pending batch - read-your-writes)
	retrieved, err := db.Get(key)
	require.NoError(err)
	require.Equal(value, retrieved)

	// Flush pending
	fwDB := db.(*Database)
	fwDB.pendingMu.Lock()
	err = fwDB.flushLocked()
	fwDB.pendingMu.Unlock()
	require.NoError(err)

	// Test Get (committed state)
	retrieved, err = db.Get(key)
	require.NoError(err)
	require.Equal(value, retrieved)

	// Test Delete
	err = db.Delete(key)
	require.NoError(err)

	// Test Has (should see delete in pending)
	has, err = db.Has(key)
	require.NoError(err)
	require.False(has)

	// Test Get (should return not found)
	_, err = db.Get(key)
	require.ErrorIs(err, database.ErrNotFound)
}

// TestFirewoodAutoFlush tests auto-flush behavior
func TestFirewoodAutoFlush(t *testing.T) {
	require := require.New(t)

	tmpDir, err := os.MkdirTemp("", "firewood-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	log := logging.NoLog{}
	db, err := New(dbPath, nil, log)
	require.NoError(err)
	defer db.Close()

	fwDB := db.(*Database)
	fwDB.flushSize = 10 // Auto-flush at 10 ops

	// Add 9 operations - should not flush
	for i := 0; i < 9; i++ {
		key := []byte{byte(i)}
		value := []byte{byte(i * 2)}
		err = db.Put(key, value)
		require.NoError(err)
	}

	// Verify pending batch has 9 ops
	fwDB.pendingMu.Lock()
	pendingCount := len(fwDB.pending.ops)
	fwDB.pendingMu.Unlock()
	require.Equal(9, pendingCount)

	// Add 10th operation - should trigger flush
	key10 := []byte{10}
	value10 := []byte{20}
	err = db.Put(key10, value10)
	require.NoError(err)

	// Verify pending batch was flushed
	fwDB.pendingMu.Lock()
	pendingCount = len(fwDB.pending.ops)
	fwDB.pendingMu.Unlock()
	require.Equal(0, pendingCount, "pending batch should be empty after auto-flush")

	// Verify data was committed
	retrieved, err := db.Get(key10)
	require.NoError(err)
	require.Equal(value10, retrieved)
}

// TestFirewoodBatch tests batch operations
func TestFirewoodBatch(t *testing.T) {
	require := require.New(t)

	tmpDir, err := os.MkdirTemp("", "firewood-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	log := logging.NoLog{}
	db, err := New(dbPath, nil, log)
	require.NoError(err)
	defer db.Close()

	// Create batch
	batch := db.NewBatch()

	// Add operations to batch
	key1 := []byte("key1")
	value1 := []byte("value1")
	err = batch.Put(key1, value1)
	require.NoError(err)

	key2 := []byte("key2")
	value2 := []byte("value2")
	err = batch.Put(key2, value2)
	require.NoError(err)

	// Batch should have size
	size := batch.Size()
	require.Greater(size, 0)

	// Write batch atomically
	err = batch.Write()
	require.NoError(err)

	// Verify both keys exist
	retrieved1, err := db.Get(key1)
	require.NoError(err)
	require.Equal(value1, retrieved1)

	retrieved2, err := db.Get(key2)
	require.NoError(err)
	require.Equal(value2, retrieved2)

	// Test batch reset
	batch.Reset()
	require.Equal(0, batch.Size())
}

// TestFirewoodCloseFlush tests that Close() flushes pending writes
func TestFirewoodCloseFlush(t *testing.T) {
	require := require.New(t)

	tmpDir, err := os.MkdirTemp("", "firewood-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	log := logging.NoLog{}
	db, err := New(dbPath, nil, log)
	require.NoError(err)

	// Add data to pending batch
	key := []byte("flush-on-close")
	value := []byte("should-be-committed")
	err = db.Put(key, value)
	require.NoError(err)

	// Close should flush pending
	err = db.Close()
	require.NoError(err)

	// Reopen database
	db2, err := New(dbPath, nil, log)
	require.NoError(err)
	defer db2.Close()

	// Verify data was committed
	retrieved, err := db2.Get(key)
	require.NoError(err)
	require.Equal(value, retrieved)
}
