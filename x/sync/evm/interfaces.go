// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package evmsync

import (
	"context"

	"github.com/ava-labs/libevm/common"
)

// SyncMode defines the execution pattern for state synchronization.
// Different chains can choose the pattern that best fits their requirements.
type SyncMode int

const (
	// ModeBlocking uses a simple blocking pattern where Sync() blocks until completion.
	// Used by coreth for simplicity and backward compatibility.
	ModeBlocking SyncMode = iota

	// ModeAsync uses an advanced async pattern with Start() + Wait() for background execution.
	// Used by subnet-evm for advanced features like stuck detection and restart support.
	ModeAsync
)

// SyncConfig contains all configuration options for state synchronization.
// Fields are applicable to different modes based on the chosen SyncMode.
type SyncConfig struct {
	// Mode determines the execution pattern (ModeBlocking or ModeAsync)
	Mode SyncMode

	// Common configuration (applies to both modes)
	WorkerCount         int    // Number of concurrent sync workers (0 = auto-calculate)
	RequestSize         uint16 // Number of leafs to request from a peer at a time
	BatchSize           int    // Write batches when they reach this size
	MaxOutstandingCodes int    // Maximum number of code hashes in the code syncer queue
	NumCodeWorkers      int    // Number of code syncing threads

	// Advanced features (only for ModeAsync)
	EnableStuckDetector bool // Enable stuck detection and automatic fallback to block sync
	EnableRestart       bool // Enable automatic restart on transient failures
	MaxRestartAttempts  int  // Maximum number of restart attempts (0 = unlimited)

	// Retry configuration
	RetryPolicy RetryConfig
}

// RetryConfig defines retry behavior for transient failures.
type RetryConfig struct {
	MaxAttempts     int  // Maximum retry attempts (0 = no retries)
	EnableBackoff   bool // Enable exponential backoff
	InitialInterval int  // Initial retry interval in milliseconds
	MaxInterval     int  // Maximum retry interval in milliseconds
	Multiplier      float64
}

// StateSyncer is the unified interface for state synchronization.
// It supports both simple blocking and advanced async patterns based on configuration.
type StateSyncer interface {
	// Sync performs state synchronization to the given root.
	// Behavior depends on SyncMode:
	//   - ModeBlocking: Blocks until sync completes or fails
	//   - ModeAsync: Starts sync in background and returns immediately
	Sync(ctx context.Context, root common.Hash) error

	// Start initiates sync in the background (ModeAsync only).
	// Returns error immediately if already started or if mode is ModeBlocking.
	Start(ctx context.Context, root common.Hash) error

	// Wait blocks until sync completes (ModeAsync only).
	// Returns error if Start() was not called first or if mode is ModeBlocking.
	Wait() error

	// Done returns a channel that is closed when sync completes.
	// Works in both modes for notification purposes.
	Done() <-chan struct{}

	// Err returns the error that caused sync to fail, or nil if successful.
	// Safe to call after Done() channel is closed or Wait() returns.
	Err() error
}

// DefaultConfig returns a sensible default configuration for the given mode.
func DefaultConfig(mode SyncMode) SyncConfig {
	base := SyncConfig{
		Mode:                mode,
		WorkerCount:         0,     // Auto-calculate based on CPU
		RequestSize:         1024,  // Reasonable default
		BatchSize:           1000,  // Balance between memory and commit frequency
		MaxOutstandingCodes: 1024,
		NumCodeWorkers:      4,
		RetryPolicy: RetryConfig{
			MaxAttempts:     3,
			EnableBackoff:   true,
			InitialInterval: 1000,  // 1 second
			MaxInterval:     30000, // 30 seconds
			Multiplier:      2.0,
		},
	}

	// Enable advanced features for ModeAsync
	if mode == ModeAsync {
		base.EnableStuckDetector = true
		base.EnableRestart = true
		base.MaxRestartAttempts = 5
	}

	return base
}
