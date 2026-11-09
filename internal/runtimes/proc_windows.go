//go:build windows

package runtimes

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps exec-mode probes from flashing a console window per language.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
