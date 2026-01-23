// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package handlers

// HandlerConfig contains common configuration for all sync handlers.
// This allows both coreth and subnet-evm to use the same handler implementation
// with version-specific parameters.
type HandlerConfig struct {
	// TrieDB is the trie database used for proof generation (interface{} to avoid import cycle)
	TrieDB interface{}

	// SnapshotProvider provides access to the snapshot tree (if available)
	SnapshotProvider interface{}

	// Codec is used for encoding/decoding messages (codec.Manager at runtime)
	Codec interface{}

	// TrieKeyLength is the expected length of trie keys (usually 32 for common.HashLength)
	TrieKeyLength int

	// Version identifies which chain version is using the handler
	// Used to handle minor differences between coreth and subnet-evm
	Version HandlerVersion
}

// HandlerVersion identifies which chain version is using the handler.
type HandlerVersion int

const (
	// VersionCoreth indicates the handler is being used by coreth
	VersionCoreth HandlerVersion = iota

	// VersionSubnetEVM indicates the handler is being used by subnet-evm
	VersionSubnetEVM
)

// SnapshotProvider provides access to the snapshot tree.
// This interface allows both coreth and subnet-evm snapshot implementations.
// Actual type will be *snapshot.Tree at runtime.
type SnapshotProvider interface {
	// Snapshots returns the snapshot tree, or nil if snapshots are not available
	Snapshots() interface{}
}

// StatsRecorder provides an interface for recording handler statistics.
// This allows different implementations (coreth vs subnet-evm) to record
// stats in their own metrics systems.
type StatsRecorder interface {
	// IncLeafsRequest increments the count of leafs requests received
	IncLeafsRequest()

	// IncInvalidLeafsRequest increments the count of invalid leafs requests
	IncInvalidLeafsRequest()

	// IncMissingRoot increments the count of requests for missing roots
	IncMissingRoot()

	// UpdateLeafsRequestProcessingTime updates the processing time for leafs requests
	UpdateLeafsRequestProcessingTime(duration int64)

	// UpdateLeafsReturned updates the count of leafs returned
	UpdateLeafsReturned(count uint16)

	// UpdateRangeProofValsReturned updates the count of range proof values returned
	UpdateRangeProofValsReturned(count int64)

	// UpdateGenerateRangeProofTime updates the time spent generating range proofs
	UpdateGenerateRangeProofTime(duration int64)

	// UpdateReadLeafsTime updates the time spent reading leafs
	UpdateReadLeafsTime(duration int64)
}

// NoOpStatsRecorder is a stats recorder that does nothing.
// Useful for testing or when stats are not needed.
type NoOpStatsRecorder struct{}

func (n *NoOpStatsRecorder) IncLeafsRequest()                            {}
func (n *NoOpStatsRecorder) IncInvalidLeafsRequest()                     {}
func (n *NoOpStatsRecorder) IncMissingRoot()                             {}
func (n *NoOpStatsRecorder) UpdateLeafsRequestProcessingTime(int64)     {}
func (n *NoOpStatsRecorder) UpdateLeafsReturned(uint16)                 {}
func (n *NoOpStatsRecorder) UpdateRangeProofValsReturned(int64)         {}
func (n *NoOpStatsRecorder) UpdateGenerateRangeProofTime(int64)         {}
func (n *NoOpStatsRecorder) UpdateReadLeafsTime(int64)                  {}

// String returns a human-readable representation of the handler version.
func (v HandlerVersion) String() string {
	switch v {
	case VersionCoreth:
		return "coreth"
	case VersionSubnetEVM:
		return "subnet-evm"
	default:
		return "unknown"
	}
}

// ValidateConfig checks if the handler configuration is valid.
func ValidateConfig(cfg HandlerConfig) error {
	if cfg.TrieDB == nil {
		return ErrNilTrieDB
	}
	if cfg.Codec == nil {
		return ErrNilCodec
	}
	if cfg.TrieKeyLength <= 0 {
		return ErrInvalidTrieKeyLength
	}
	return nil
}

// Note: Codec interface check is runtime type assertion since we use interface{}
// for TrieDB to avoid import cycles. Actual implementations will use concrete types.

// Common constants used across handlers
const (
	// MaxLeavesLimit is the maximum number of leaves to return in a single response.
	// This overrides any larger limit specified in the request.
	MaxLeavesLimit = uint16(1024)

	// MaxSnapshotReadTimePercent is the maximum percent of deadline time to spend
	// optimistically reading from snapshot.
	MaxSnapshotReadTimePercent = 75

	// SegmentLen is the size of segments when dividing snapshot data.
	SegmentLen = 64
)

// Common errors
var (
	ErrNilTrieDB            = &HandlerError{"trie database cannot be nil"}
	ErrNilCodec             = &HandlerError{"codec cannot be nil"}
	ErrInvalidTrieKeyLength = &HandlerError{"trie key length must be positive"}
)

// HandlerError represents an error in handler configuration or operation.
type HandlerError struct {
	message string
}

func (e *HandlerError) Error() string {
	return e.message
}
