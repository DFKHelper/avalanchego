//go:build windows
// +build windows

// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package tmpnet

import (
	"os/exec"
)

// configureDetachedProcess configures a command to run detached on Windows
// On Windows, processes are automatically detached by default, so this is a no-op
func configureDetachedProcess(cmd *exec.Cmd) {
	// No-op on Windows - processes don't inherit the parent's process group by default
	// The CREATE_NEW_PROCESS_GROUP flag is set automatically when using exec.Command
}
