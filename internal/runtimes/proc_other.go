//go:build !windows

package runtimes

import "os/exec"

// hideWindow is a no-op outside Windows.
func hideWindow(cmd *exec.Cmd) {}
