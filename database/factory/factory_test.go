// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build !windows
// +build !windows

package factory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/database/firewood"
	"github.com/ava-labs/avalanchego/utils/logging"
)

func TestFactoryFirewood(t *testing.T) {
	require := require.New(t)

	tmpDir, err := os.MkdirTemp("", "factory-firewood-*")
	require.NoError(err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	log := logging.NoLog{}
	reg := prometheus.NewRegistry()

	// Create Firewood database via factory
	db, err := New(firewood.Name, dbPath, false, nil, reg, log)
	require.NoError(err)
	require.NotNil(db)
	defer db.Close()

	// Test basic operation
	key := []byte("test-key")
	value := []byte("test-value")

	err = db.Put(key, value)
	require.NoError(err)

	retrieved, err := db.Get(key)
	require.NoError(err)
	require.Equal(value, retrieved)
}
