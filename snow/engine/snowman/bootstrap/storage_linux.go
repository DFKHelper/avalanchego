//go:build linux
// +build linux

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bootstrap

import (
	"fmt"
	"runtime"
	"syscall"
)

// getAvailableMemory returns the currently available system memory in bytes.
// This is used to determine if there's sufficient memory for database compaction.
func getAvailableMemory() (uint64, error) {
	var sysinfo syscall.Sysinfo_t
	if err := syscall.Sysinfo(&sysinfo); err != nil {
		return 0, fmt.Errorf("failed to get system info: %w", err)
	}

	// Calculate available memory: free RAM + buffers + cached
	// sysinfo.Freeram is truly free memory
	// On Linux, buffers/cache can be reclaimed, so total available = freeram
	available := uint64(sysinfo.Freeram) * uint64(sysinfo.Unit)

	// Force garbage collection to ensure we have accurate Go heap stats
	runtime.GC()

	return available, nil
}
