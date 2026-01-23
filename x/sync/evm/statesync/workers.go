// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package statesync

import (
	"runtime"
	"sync"
)

const (
	// Segment threshold constants for adaptive sizing (Coreth)
	baseSegmentThreshold = 500_000   // Default for medium-memory systems
	lowMemThreshold      = 250_000   // Conservative for <8GB
	highMemThreshold     = 1_000_000 // Aggressive for >=16GB systems

	// Memory tier thresholds in GB
	lowMemoryGB  = 8
	highMemoryGB = 16

	// Worker count constants
	minNumWorkers     = 8  // Coreth: minimum even on low-CPU systems
	maxNumWorkers     = 32 // Maximum to prevent resource exhaustion
	defaultNumWorkers = 12 // Fallback if CPU detection fails
)

var (
	// Cache segment threshold (doesn't change during runtime)
	cachedSegmentThreshold       uint64
	segmentThresholdComputedOnce sync.Once

	// Cache worker count (doesn't change during runtime)
	cachedNumWorkersCoreth   int
	cachedNumWorkersSubnetEVM int
	numWorkersCorethOnce     sync.Once
	numWorkersSubnetEVMOnce  sync.Once
)

// calculateAdaptiveSegmentThreshold returns segment size based on available memory (Coreth feature)
// Caches result since system memory doesn't change during runtime
func calculateAdaptiveSegmentThreshold() uint64 {
	segmentThresholdComputedOnce.Do(func() {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		// Sys includes all memory obtained from OS
		totalMemoryGB := float64(memStats.Sys) / (1024 * 1024 * 1024)

		switch {
		case totalMemoryGB < lowMemoryGB:
			// Conservative for low-memory nodes
			cachedSegmentThreshold = lowMemThreshold
		case totalMemoryGB >= highMemoryGB:
			// Aggressive for high-memory nodes
			cachedSegmentThreshold = highMemThreshold
		default:
			// Default for medium-memory nodes
			cachedSegmentThreshold = baseSegmentThreshold
		}
	})
	return cachedSegmentThreshold
}

// calculateOptimalWorkers determines worker count based on mode and CPU cores
func calculateOptimalWorkers(mode SyncMode) int {
	switch mode {
	case ModeBlocking:
		return calculateCorethWorkers()
	case ModeAsync:
		return calculateSubnetEVMWorkers()
	default:
		return defaultNumWorkers
	}
}

// calculateCorethWorkers uses direct CPU count with bounds [8, 32]
// Caches result since CPU count doesn't change
func calculateCorethWorkers() int {
	numWorkersCorethOnce.Do(func() {
		numCPU := runtime.NumCPU()

		switch {
		case numCPU <= 4:
			// Conservative for low-CPU systems
			cachedNumWorkersCoreth = minNumWorkers
		case numCPU >= 24:
			// Cap at max for high-CPU systems
			cachedNumWorkersCoreth = maxNumWorkers
		default:
			// Scale linearly with CPU count
			cachedNumWorkersCoreth = numCPU
			if cachedNumWorkersCoreth < minNumWorkers {
				cachedNumWorkersCoreth = minNumWorkers
			}
			if cachedNumWorkersCoreth > maxNumWorkers {
				cachedNumWorkersCoreth = maxNumWorkers
			}
		}
	})
	return cachedNumWorkersCoreth
}

// calculateSubnetEVMWorkers uses 75% of cores with bounds [4, 32]
// Caches result since CPU count doesn't change
func calculateSubnetEVMWorkers() int {
	numWorkersSubnetEVMOnce.Do(func() {
		cores := runtime.NumCPU()

		// Use 75% of cores (leaves headroom for networking, database, etc.)
		workers := cores * 3 / 4

		// Enforce bounds
		if workers < 4 {
			workers = 4
		}
		if workers > 32 {
			workers = 32
		}

		cachedNumWorkersSubnetEVM = workers
	})
	return cachedNumWorkersSubnetEVM
}
