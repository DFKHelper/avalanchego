// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package statesync

import (
	"context"
	"time"

	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/ethdb"
)

// SyncMode determines whether state sync blocks or runs async
type SyncMode int

const (
	ModeBlocking SyncMode = iota // Coreth: Sync() blocks until complete
	ModeAsync                     // Subnet-EVM: Start() then Wait()
)

// StateSyncConfig contains all configuration for the state syncer
type StateSyncConfig struct {
	Mode   SyncMode
	Root   common.Hash
	Client interface{} // syncclient.Client - avoid import cycle
	DB     ethdb.Database

	// Worker configuration
	NumWorkers          int    // 0 = auto-calculate based on CPU
	UseAdaptiveSegments bool   // Coreth: adjust segment size based on memory
	SegmentThreshold    uint64 // Override if UseAdaptiveSegments = false

	// Optimization
	MainTrieOverlapThreshold float64 // 0.95 = start storage at 95%, 1.0 = wait 100%

	// Code syncing (abstract the difference between CodeQueue and codeSyncer)
	CodeSyncer CodeSyncInterface

	// Request configuration
	RequestSize uint16
	BatchSize   int

	// Advanced features (Subnet-EVM only)
	EnableStuckDetection bool
	EnableRestart        bool
	MaxRestartAttempts   int

	// Code fetching (Subnet-EVM)
	MaxOutstandingCodeHashes int
	NumCodeFetchingWorkers   int
}

// DefaultCorethConfig returns configuration for coreth-style state sync
func DefaultCorethConfig(client interface{}, db ethdb.Database, root common.Hash, codeSync CodeSyncInterface) StateSyncConfig {
	return StateSyncConfig{
		Mode:                     ModeBlocking,
		Client:                   client,
		DB:                       db,
		Root:                     root,
		CodeSyncer:               codeSync,
		NumWorkers:               0, // auto-calculate
		UseAdaptiveSegments:      true,
		MainTrieOverlapThreshold: 0.95, // 95% overlap optimization
		RequestSize:              1024,
		BatchSize:                ethdb.IdealBatchSize * 8,
		EnableStuckDetection:     false,
		EnableRestart:            false,
	}
}

// DefaultSubnetEVMConfig returns configuration for subnet-evm style state sync
func DefaultSubnetEVMConfig(client interface{}, db ethdb.Database, root common.Hash, codeSync CodeSyncInterface) StateSyncConfig {
	return StateSyncConfig{
		Mode:                     ModeAsync,
		Client:                   client,
		DB:                       db,
		Root:                     root,
		CodeSyncer:               codeSync,
		NumWorkers:               0, // auto-calculate
		UseAdaptiveSegments:      false,
		SegmentThreshold:         500_000,
		MainTrieOverlapThreshold: 1.0, // wait for 100% before storage tries
		RequestSize:              1024,
		BatchSize:                ethdb.IdealBatchSize,
		EnableStuckDetection:     true,
		EnableRestart:            true,
		MaxRestartAttempts:       5,
		MaxOutstandingCodeHashes: 1024,
		NumCodeFetchingWorkers:   32,
	}
}

// CodeSyncInterface abstracts the difference between Coreth's CodeQueue and Subnet-EVM's codeSyncer
type CodeSyncInterface interface {
	// Start begins code fetching (may be no-op if already started)
	Start(ctx context.Context)

	// Done returns a channel that receives an error when code sync completes
	// For Coreth (which doesn't use this pattern), returns a dummy channel
	Done() <-chan error

	// NotifyAccountTrieCompleted signals that the account trie finished
	// This allows code sync to finalize (Coreth) or track progress (Subnet-EVM)
	NotifyAccountTrieCompleted()
}

// StateSyncer is the main interface for state synchronization
type StateSyncer interface {
	// Finalize writes any buffered data to disk (for crash recovery)
	Finalize() error

	// Sync performs blocking synchronization (works for both modes)
	// For ModeBlocking: runs sync and blocks until complete
	// For ModeAsync: calls Start() then Wait()
	Sync(ctx context.Context) error

	// Start begins async synchronization (ModeAsync only)
	// Returns immediately after launching background goroutines
	Start(ctx context.Context) error

	// Wait blocks until async synchronization completes (ModeAsync only)
	// Can be called multiple times, returns cached result
	Wait(ctx context.Context) error

	// Restart reinitializes for another attempt (ModeAsync with EnableRestart only)
	// Called by reviver when state sync fails but should retry
	Restart(ctx context.Context, startReqID uint32) error
}

// WorkerConfig holds calculated worker and segment settings
type WorkerConfig struct {
	NumWorkers       int
	SegmentThreshold uint64
}

// CalculateWorkerConfig determines optimal workers and segment threshold
func CalculateWorkerConfig(config StateSyncConfig) WorkerConfig {
	workers := config.NumWorkers
	if workers <= 0 {
		workers = calculateOptimalWorkers(config.Mode)
	}

	// Enforce minimum to prevent deadlock (main trie + storage tries need 2+ workers)
	if workers < 2 {
		workers = 2
	}

	threshold := config.SegmentThreshold
	if config.UseAdaptiveSegments {
		threshold = calculateAdaptiveSegmentThreshold()
	}
	if threshold == 0 {
		threshold = 500_000 // default
	}

	return WorkerConfig{
		NumWorkers:       workers,
		SegmentThreshold: threshold,
	}
}

// StuckDetectorConfig holds stuck detection settings
type StuckDetectorConfig struct {
	Enabled                bool
	CheckInterval          time.Duration
	MinLeafsPerSecond      float64
	MinTriesPerMinute      float64
	StallDuration          time.Duration
	CodeSyncStallDuration  time.Duration
	ProgressHistoryWindow  int
}

// DefaultStuckDetectorConfig returns default stuck detection settings
func DefaultStuckDetectorConfig() StuckDetectorConfig {
	return StuckDetectorConfig{
		Enabled:                true,
		CheckInterval:          30 * time.Second,
		MinLeafsPerSecond:      100,
		MinTriesPerMinute:      1,
		StallDuration:          5 * time.Minute,
		CodeSyncStallDuration:  2 * time.Minute,
		ProgressHistoryWindow:  10,
	}
}
