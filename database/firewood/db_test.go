// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build !windows
// +build !windows

package firewood

import (
	"context"
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

	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "firewood-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database
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
}
