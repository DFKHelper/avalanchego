//go:build windows
// +build windows

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bootstrap

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// getAvailableMemory returns the currently available system memory in bytes.
// This is used to determine if there's sufficient memory for database compaction.
func getAvailableMemory() (uint64, error) {
	var memStatus memoryStatusEx
	memStatus.Length = uint32(unsafe.Sizeof(memStatus))

	ret, _, err := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&memStatus)))
	if ret == 0 {
		return 0, fmt.Errorf("failed to get system info: %w", err)
	}

	// Force garbage collection to ensure we have accurate Go heap stats
	runtime.GC()

	return memStatus.AvailPhys, nil
}
