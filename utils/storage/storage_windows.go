//go:build windows
// +build windows

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package storage

import (
	"errors"
	"syscall"
	"unsafe"
)

var (
	errZeroAvailableBytes = errors.New("available blocks is reported as 0")
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

func AvailableBytes(storagePath string) (uint64, uint64, error) {
	var freeBytesAvailable uint64
	var totalNumberOfBytes uint64
	var totalNumberOfFreeBytes uint64

	pathPtr, err := syscall.UTF16PtrFromString(storagePath)
	if err != nil {
		return 0, 0, err
	}

	r1, _, err := procGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)

	if r1 == 0 {
		return 0, 0, err
	}

	if totalNumberOfBytes == 0 {
		return 0, 0, errZeroAvailableBytes
	}

	percentage := freeBytesAvailable * 100 / totalNumberOfBytes
	return freeBytesAvailable, percentage, nil
}
