// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package leveldb

import (
	"errors"
	"fmt"
	"runtime"
)

var (
	ErrMemoryConfigTooHigh       = errors.New("database memory config exceeds safe limits")
	ErrInsufficientSystemMemory  = errors.New("insufficient system memory for database config")
	ErrMemoryPressureWarning     = errors.New("database config may cause memory pressure")
)

// MemoryPressureConfig defines limits for database memory usage validation
type MemoryPressureConfig struct {
	// MaxMemoryUsagePercent is the maximum percentage of system memory that
	// the database config is allowed to use (default: 50%)
	MaxMemoryUsagePercent float64

	// WarnMemoryUsagePercent is the threshold for issuing warnings (default: 30%)
	WarnMemoryUsagePercent float64
}

// DefaultMemoryPressureConfig returns sensible defaults for memory validation
func DefaultMemoryPressureConfig() MemoryPressureConfig {
	return MemoryPressureConfig{
		MaxMemoryUsagePercent:  0.50, // 50% of system memory
		WarnMemoryUsagePercent: 0.30, // 30% of system memory
	}
}

// ValidateConfig checks if the provided database configuration will cause
// excessive memory pressure on the system.
//
// It estimates total memory usage from:
// - BlockCacheCapacity (direct memory usage)
// - WriteBuffer * 2 (LevelDB holds up to 2 memtables)
// - OpenFilesCacheCapacity * ~4KB (approximate file descriptor overhead)
//
// Returns an error if estimated usage exceeds MaxMemoryUsagePercent of available memory.
func ValidateConfig(cfg config, pressureCfg MemoryPressureConfig) error {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Estimate total database memory usage
	estimatedMemory := estimateDatabaseMemory(cfg)

	// Get available system memory
	// Use HeapSys as a proxy for available memory (conservative estimate)
	availableMemory := m.Sys
	if availableMemory == 0 {
		// Fallback if system memory detection fails
		return fmt.Errorf("unable to detect system memory")
	}

	usagePercent := float64(estimatedMemory) / float64(availableMemory)

	// Check against maximum threshold
	if usagePercent > pressureCfg.MaxMemoryUsagePercent {
		return fmt.Errorf(
			"%w: estimated %d MB (%.1f%% of %d MB available) exceeds maximum %.1f%%",
			ErrMemoryConfigTooHigh,
			estimatedMemory/(1024*1024),
			usagePercent*100,
			availableMemory/(1024*1024),
			pressureCfg.MaxMemoryUsagePercent*100,
		)
	}

	// Issue warning if above warning threshold
	if usagePercent > pressureCfg.WarnMemoryUsagePercent {
		return fmt.Errorf(
			"%w: estimated %d MB (%.1f%% of %d MB available) exceeds recommended %.1f%%",
			ErrMemoryPressureWarning,
			estimatedMemory/(1024*1024),
			usagePercent*100,
			availableMemory/(1024*1024),
			pressureCfg.WarnMemoryUsagePercent*100,
		)
	}

	return nil
}

// estimateDatabaseMemory calculates the approximate memory footprint of a LevelDB configuration
func estimateDatabaseMemory(cfg config) uint64 {
	var total uint64

	// Block cache (direct allocation)
	total += uint64(cfg.BlockCacheCapacity)

	// Write buffers (LevelDB can hold up to 2 memtables simultaneously)
	total += uint64(cfg.WriteBuffer) * 2

	// File descriptor cache overhead
	// Each open file descriptor has overhead for OS buffers, metadata, etc.
	// Conservative estimate: ~4KB per file descriptor
	const fileDescriptorOverhead = 4 * 1024
	total += uint64(cfg.OpenFilesCacheCapacity) * fileDescriptorOverhead

	// Compaction overhead (temporary, but significant during active compaction)
	// During compaction, LevelDB reads multiple sorted tables into memory
	// Conservative estimate: 2x table size
	total += uint64(cfg.CompactionTableSize) * 2

	return total
}

// GetMemoryEstimate returns a human-readable estimate of memory usage for the config
func GetMemoryEstimate(cfg config) string {
	estimatedBytes := estimateDatabaseMemory(cfg)
	estimatedMB := estimatedBytes / (1024 * 1024)
	estimatedGB := float64(estimatedBytes) / (1024 * 1024 * 1024)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	availableMB := m.Sys / (1024 * 1024)

	usagePercent := float64(estimatedBytes) / float64(m.Sys) * 100

	if estimatedGB >= 1.0 {
		return fmt.Sprintf("%.2f GB (~%d MB, %.1f%% of %d MB available)",
			estimatedGB, estimatedMB, usagePercent, availableMB)
	}
	return fmt.Sprintf("%d MB (%.1f%% of %d MB available)",
		estimatedMB, usagePercent, availableMB)
}
