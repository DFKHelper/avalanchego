// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package firewood

import (
	"github.com/ava-labs/firewood-go-ethhash/ffi"
)

const (
	// DefaultCacheSizeBytes is the default size for Firewood's cache
	// Merkle trie databases benefit from caching frequently accessed nodes
	DefaultCacheSizeBytes = 512 * 1024 * 1024 // 512 MB

	// DefaultFreeListCacheEntries is the default number of free list entries to cache
	// Firewood uses a free list for memory management
	DefaultFreeListCacheEntries = 1024

	// DefaultRevisionsInMemory is the default number of historical revisions to keep
	// Firewood supports versioned storage - this controls memory vs disk trade-off
	DefaultRevisionsInMemory = 10
)

// Config defines configuration options for Firewood database
//
// Firewood is a merkle trie database optimized for blockchain state storage.
// It provides:
// - Built-in merkle proof generation
// - Versioned storage (historical state queries)
// - Efficient trie pruning
// - Memory-mapped I/O for performance
type Config struct {
	// CacheSizeBytes controls the size of the in-memory node cache
	// Larger values improve read performance but increase memory usage
	// Recommended: 512 MB - 2 GB depending on available RAM
	CacheSizeBytes uint `json:"cacheSizeBytes"`

	// FreeListCacheEntries controls free list caching for allocation efficiency
	// Higher values reduce allocation overhead at cost of memory
	// Recommended: 1024 - 4096
	FreeListCacheEntries uint `json:"freeListCacheEntries"`

	// RevisionsInMemory controls how many historical revisions to keep in memory
	// Higher values allow faster historical queries but increase memory usage
	// Set to 0 to disable historical queries (lowest memory usage)
	// Recommended: 10 for most use cases, 0 for constrained systems
	RevisionsInMemory uint `json:"revisionsInMemory"`

	// CacheStrategy determines eviction policy for the node cache
	// Uses FFI CacheStrategy type from firewood-go-ethhash
	CacheStrategy ffi.CacheStrategy `json:"cacheStrategy"`

	// FlushSize controls auto-flush threshold for pending writes
	// When pending operations reach this count, they are automatically committed
	// Higher values = better batch efficiency but more memory
	// Lower values = lower memory but more frequent commits
	// Recommended: 1000 for most use cases
	FlushSize int `json:"flushSize"`
}

// DefaultConfig returns the default Firewood configuration
func DefaultConfig() Config {
	return Config{
		CacheSizeBytes:       DefaultCacheSizeBytes,
		FreeListCacheEntries: DefaultFreeListCacheEntries,
		RevisionsInMemory:    DefaultRevisionsInMemory,
		CacheStrategy:        ffi.CacheAllReads, // Default: cache all reads
		FlushSize:            DefaultFlushSize,
	}
}
