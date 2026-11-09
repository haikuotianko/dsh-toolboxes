//go:build windows

package runtimes

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// configuredPathDirs returns the PATH that is configured *now*, from the
// machine and user environment blocks. A long-running process keeps the PATH it
// inherited at start-up, so installing pwsh 7 (or any tool) after that leaves
// the process blind to it even though a new shell would find it.
func configuredPathDirs() []string {
	var values []string
	machineKey, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE)
	if err == nil {
		if value, _, valueErr := machineKey.GetStringValue("Path"); valueErr == nil {
			values = append(values, value)
		}
		_ = machineKey.Close()
	}
	userKey, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err == nil {
		if value, _, valueErr := userKey.GetStringValue("Path"); valueErr == nil {
			values = append(values, value)
		}
		_ = userKey.Close()
	}
	return values
}

// wellKnownDirs are install locations that do not always reach PATH.
func wellKnownDirs() []string {
	var out []string
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles(x86)")} {
		if base == "" {
			continue
		}
		out = append(out, filepath.Join(base, "PowerShell", "7"), filepath.Join(base, "PowerShell", "7-preview"))
	}
	// WindowsApps is deliberately not listed: it holds Store "app execution
	// aliases", which are 0-byte reparse points that cannot be launched by path.
	return out
}
