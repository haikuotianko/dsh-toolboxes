//go:build !windows

package runtimes

// configuredPathDirs is empty outside Windows: the process environment is the
// only PATH there, and installers do not rewrite it behind our back.
func configuredPathDirs() []string { return nil }

// wellKnownDirs outside Windows: package managers put their shims on PATH.
func wellKnownDirs() []string { return nil }
