//go:build darwin
// +build darwin

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bootstrap

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// getAvailableMemory returns the currently available system memory in bytes.
// This is used to determine if there's sufficient memory for database compaction.
func getAvailableMemory() (uint64, error) {
	// On macOS/Darwin, use sysctl to get memory info

	// Get page size
	pageSize, err := syscall.SysctlUint32("hw.pagesize")
	if err != nil {
		return 0, fmt.Errorf("failed to get page size: %w", err)
	}

	// Get free page count
	// Note: vm.page_free_count might require root on some macOS versions
	freePages, err := sysctlUint64("vm.page_free_count")
	if err != nil {
		// Fallback: use total memory as a conservative estimate
		// Assume 50% of total physical memory is available
		totalPhys, err := sysctlUint64("hw.memsize")
		if err != nil {
			return 0, fmt.Errorf("failed to get memory info: %w", err)
		}
		runtime.GC()
		return totalPhys / 2, nil
	}

	available := uint64(freePages) * uint64(pageSize)

	// Force garbage collection to ensure we have accurate Go heap stats
	runtime.GC()

	return available, nil
}

// Helper function for sysctl calls that return uint64
func sysctlUint64(name string) (uint64, error) {
	s, err := syscall.Sysctl(name)
	if err != nil {
		return 0, err
	}
	if len(s) != 8 {
		return 0, fmt.Errorf("unexpected sysctl value length for %s", name)
	}
	return *(*uint64)(unsafe.Pointer(&s[0])), nil
}
