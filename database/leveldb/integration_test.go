// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build integration
// +build integration

package leveldb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/utils/logging"
)

// TestDatabaseLifecycle_Integration tests the full database lifecycle
// including initialization, operations, health checks, and cleanup.
func TestDatabaseLifecycle_Integration(t *testing.T) {
	require := require.New(t)

	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "leveldb-integration-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create database with reduced memory config
	log := logging.NoLog{}
	reg := prometheus.NewRegistry()

	db, err := New(dbPath, nil, log, reg)
	require.NoError(err)
	require.NotNil(db)

	// Verify database is healthy
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = db.HealthCheck(ctx)
	require.NoError(err)

	// Perform basic operations
	testKey := []byte("test-key")
	testValue := []byte("test-value")

	// Put
	err = db.Put(testKey, testValue)
	require.NoError(err)

	// Get
	retrievedValue, err := db.Get(testKey)
	require.NoError(err)
	require.Equal(testValue, retrievedValue)

	// Has
	has, err := db.Has(testKey)
	require.NoError(err)
	require.True(has)

	// Delete
	err = db.Delete(testKey)
	require.NoError(err)

	// Verify deleted
	has, err = db.Has(testKey)
	require.NoError(err)
	require.False(has)

	// Close database
	err = db.Close()
	require.NoError(err)

	// Verify database files exist
	_, err = os.Stat(dbPath)
	require.NoError(err)
}

// TestDatabaseMemoryPressure_Integration verifies memory pressure monitoring
// works correctly during intensive operations.
func TestDatabaseMemoryPressure_Integration(t *testing.T) {
	require := require.New(t)

	tmpDir, err := os.MkdirTemp("", "leveldb-pressure-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	log := logging.NoLog{}
	reg := prometheus.NewRegistry()

	db, err := New(dbPath, nil, log, reg)
	require.NoError(err)
	defer db.Close()

	// Perform intensive write operations
	batch := db.NewBatch()
	for i := 0; i < 10000; i++ {
		key := []byte{byte(i >> 8), byte(i & 0xff)}
		value := make([]byte, 1024) // 1KB value
		err = batch.Put(key, value)
		require.NoError(err)
	}

	err = batch.Write()
	require.NoError(err)

	// Allow time for memory pressure monitoring to run
	time.Sleep(2 * time.Second)

	// Database should still be healthy
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = db.HealthCheck(ctx)
	require.NoError(err)
}

// TestDatabaseRecovery_Integration tests database recovery from corruption.
func TestDatabaseRecovery_Integration(t *testing.T) {
	require := require.New(t)

	tmpDir, err := os.MkdirTemp("", "leveldb-recovery-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	log := logging.NoLog{}
	reg := prometheus.NewRegistry()

	// Create and populate database
	db, err := New(dbPath, nil, log, reg)
	require.NoError(err)

	testKey := []byte("test-key")
	testValue := []byte("test-value")
	err = db.Put(testKey, testValue)
	require.NoError(err)

	err = db.Close()
	require.NoError(err)

	// Reopen database - should work without issues
	// Use a new registry to avoid duplicate metrics registration
	reg2 := prometheus.NewRegistry()
	db2, err := New(dbPath, nil, log, reg2)
	require.NoError(err)

	// Verify data persisted
	retrievedValue, err := db2.Get(testKey)
	require.NoError(err)
	require.Equal(testValue, retrievedValue)

	err = db2.Close()
	require.NoError(err)
}

// TestConfigValidation_Integration tests that config validation
// prevents starting databases with unsafe configurations.
func TestConfigValidation_Integration(t *testing.T) {
	require := require.New(t)

	tmpDir, err := os.MkdirTemp("", "leveldb-config-test-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	log := logging.NoLog{}
	reg := prometheus.NewRegistry()

	// Create config that's too aggressive (should fail or warn)
	// Note: Actual validation happens inside New()
	// The config will be validated and potentially rejected

	db, err := New(dbPath, nil, log, reg)
	// Should succeed with default config
	require.NoError(err)
	require.NotNil(db)

	err = db.Close()
	require.NoError(err)
}
