//go:build windows
// +build windows

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package ulimit

import (
	"github.com/ava-labs/avalanchego/utils/logging"
)

const DefaultFDLimit = 32 * 1024

// Set is a no-op on Windows as file descriptor limits are handled differently
func Set(limit uint64, log logging.Logger) error {
	// Windows doesn't have the same ulimit concept as Unix systems
	// File handles are managed differently by the Windows kernel
	return nil
}
