// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package statesync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/libevm/common"
)

// Mock database for testing
type mockDB struct{}

func (m *mockDB) Get(key []byte) ([]byte, error)              { return nil, nil }
func (m *mockDB) Has(key []byte) (bool, error)                { return false, nil }
func (m *mockDB) Put(key []byte, value []byte) error          { return nil }
func (m *mockDB) Delete(key []byte) error                     { return nil }
func (m *mockDB) NewBatch() interface{}                       { return nil }
func (m *mockDB) NewBatchWithSize(size int) interface{}       { return nil }
func (m *mockDB) NewIterator(prefix []byte, start []byte) interface{} { return nil }
func (m *mockDB) Stat(property string) (string, error)        { return "", nil }
func (m *mockDB) Compact(start []byte, limit []byte) error    { return nil }
func (m *mockDB) Close() error                                { return nil }
func (m *mockDB) HealthCheck(ctx context.Context) (interface{}, error) { return nil, nil }

// Mock code syncer
type mockCodeSyncer struct {
	doneCh chan error
}

func newMockCodeSyncer() *mockCodeSyncer {
	return &mockCodeSyncer{
		doneCh: make(chan error),
	}
}

func (m *mockCodeSyncer) Start(ctx context.Context) {
	// No-op
}

func (m *mockCodeSyncer) Done() <-chan error {
	return m.doneCh
}

func (m *mockCodeSyncer) NotifyAccountTrieCompleted() {
	close(m.doneCh)
}

func TestNewStateSyncer_InvalidConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      StateSyncConfig
		expectError string
	}{
		{
			name: "missing database",
			config: StateSyncConfig{
				Client:      &struct{}{},
				CodeSyncer:  newMockCodeSyncer(),
				RequestSize: 1024,
			},
			expectError: "database required",
		},
		{
			name: "missing client",
			config: StateSyncConfig{
				DB:          &mockDB{},
				CodeSyncer:  newMockCodeSyncer(),
				RequestSize: 1024,
			},
			expectError: "client required",
		},
		{
			name: "missing code syncer",
			config: StateSyncConfig{
				DB:          &mockDB{},
				Client:      &struct{}{},
				RequestSize: 1024,
			},
			expectError: "code syncer required",
		},
		{
			name: "zero request size",
			config: StateSyncConfig{
				DB:          &mockDB{},
				Client:      &struct{}{},
				CodeSyncer:  newMockCodeSyncer(),
				RequestSize: 0,
			},
			expectError: "request size must be > 0",
		},
		{
			name: "restart enabled but zero max attempts",
			config: StateSyncConfig{
				Mode:               ModeAsync,
				DB:                 &mockDB{},
				Client:             &struct{}{},
				CodeSyncer:         newMockCodeSyncer(),
				RequestSize:        1024,
				EnableRestart:      true,
				MaxRestartAttempts: 0,
			},
			expectError: "max restart attempts must be > 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer, err := NewStateSyncer(tt.config)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.expectError)
			require.Nil(t, syncer)
		})
	}
}

func TestNewStateSyncer_ValidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config StateSyncConfig
	}{
		{
			name: "blocking mode (coreth)",
			config: StateSyncConfig{
				Mode:        ModeBlocking,
				DB:          &mockDB{},
				Client:      &struct{}{},
				CodeSyncer:  newMockCodeSyncer(),
				RequestSize: 1024,
				BatchSize:   1024,
			},
		},
		{
			name: "async mode (subnet-evm)",
			config: StateSyncConfig{
				Mode:               ModeAsync,
				DB:                 &mockDB{},
				Client:             &struct{}{},
				CodeSyncer:         newMockCodeSyncer(),
				RequestSize:        1024,
				BatchSize:          1024,
				EnableStuckDetection: true,
				EnableRestart:      true,
				MaxRestartAttempts: 5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer, err := NewStateSyncer(tt.config)
			require.NoError(t, err)
			require.NotNil(t, syncer)
		})
	}
}

func TestStateSyncer_StartWaitOnlyInAsyncMode(t *testing.T) {
	// Blocking mode - Start/Wait should error
	config := StateSyncConfig{
		Mode:        ModeBlocking,
		DB:          &mockDB{},
		Client:      &struct{}{},
		CodeSyncer:  newMockCodeSyncer(),
		RequestSize: 1024,
		BatchSize:   1024,
	}

	syncer, err := NewStateSyncer(config)
	require.NoError(t, err)

	ctx := context.Background()

	err = syncer.Start(ctx)
	require.ErrorIs(t, err, errNotAsyncMode)

	err = syncer.Wait(ctx)
	require.ErrorIs(t, err, errNotAsyncMode)
}

func TestStateSyncer_RestartRequiresAsyncAndEnabled(t *testing.T) {
	tests := []struct {
		name        string
		config      StateSyncConfig
		expectError error
	}{
		{
			name: "blocking mode",
			config: StateSyncConfig{
				Mode:        ModeBlocking,
				DB:          &mockDB{},
				Client:      &struct{}{},
				CodeSyncer:  newMockCodeSyncer(),
				RequestSize: 1024,
				BatchSize:   1024,
			},
			expectError: errNotAsyncMode,
		},
		{
			name: "async mode but restart disabled",
			config: StateSyncConfig{
				Mode:          ModeAsync,
				DB:            &mockDB{},
				Client:        &struct{}{},
				CodeSyncer:    newMockCodeSyncer(),
				RequestSize:   1024,
				BatchSize:     1024,
				EnableRestart: false,
			},
			expectError: errRestartDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer, err := NewStateSyncer(tt.config)
			require.NoError(t, err)

			ctx := context.Background()
			err = syncer.Restart(ctx, 1)
			require.ErrorIs(t, err, tt.expectError)
		})
	}
}

func TestDefaultConfigs(t *testing.T) {
	db := &mockDB{}
	client := &struct{}{}
	root := common.Hash{}
	codeSync := newMockCodeSyncer()

	t.Run("coreth config", func(t *testing.T) {
		config := DefaultCorethConfig(client, db, root, codeSync)

		require.Equal(t, ModeBlocking, config.Mode)
		require.True(t, config.UseAdaptiveSegments)
		require.Equal(t, 0.95, config.MainTrieOverlapThreshold)
		require.False(t, config.EnableStuckDetection)
		require.False(t, config.EnableRestart)
	})

	t.Run("subnet-evm config", func(t *testing.T) {
		config := DefaultSubnetEVMConfig(client, db, root, codeSync)

		require.Equal(t, ModeAsync, config.Mode)
		require.False(t, config.UseAdaptiveSegments)
		require.Equal(t, uint64(500_000), config.SegmentThreshold)
		require.Equal(t, 1.0, config.MainTrieOverlapThreshold)
		require.True(t, config.EnableStuckDetection)
		require.True(t, config.EnableRestart)
		require.Equal(t, 5, config.MaxRestartAttempts)
	})
}

func TestCalculateWorkerConfig(t *testing.T) {
	t.Run("blocking mode", func(t *testing.T) {
		config := StateSyncConfig{
			Mode:            ModeBlocking,
			UseAdaptiveSegments: true,
			NumWorkers:      0, // auto-calculate
		}

		workerCfg := CalculateWorkerConfig(config)

		require.GreaterOrEqual(t, workerCfg.NumWorkers, 2)
		require.GreaterOrEqual(t, workerCfg.SegmentThreshold, uint64(250_000))
	})

	t.Run("async mode", func(t *testing.T) {
		config := StateSyncConfig{
			Mode:             ModeAsync,
			UseAdaptiveSegments: false,
			SegmentThreshold: 500_000,
			NumWorkers:       0, // auto-calculate
		}

		workerCfg := CalculateWorkerConfig(config)

		require.GreaterOrEqual(t, workerCfg.NumWorkers, 2)
		require.Equal(t, uint64(500_000), workerCfg.SegmentThreshold)
	})

	t.Run("explicit worker count", func(t *testing.T) {
		config := StateSyncConfig{
			Mode:       ModeBlocking,
			NumWorkers: 16,
		}

		workerCfg := CalculateWorkerConfig(config)

		require.Equal(t, 16, workerCfg.NumWorkers)
	})

	t.Run("enforces minimum 2 workers", func(t *testing.T) {
		config := StateSyncConfig{
			Mode:       ModeBlocking,
			NumWorkers: 1, // Too low
		}

		workerCfg := CalculateWorkerConfig(config)

		require.Equal(t, 2, workerCfg.NumWorkers)
	})
}
