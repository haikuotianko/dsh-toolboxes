package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// RuntimeView is one programming runtime found on this machine.
type RuntimeView struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
}

type runtimeProbe struct {
	name    string
	command string
	args    []string
}

var runtimeProbes = []runtimeProbe{
	{"node", "node", []string{"--version"}},
	{"python", "python", []string{"--version"}},
	{"python3", "python3", []string{"--version"}},
	{"go", "go", []string{"version"}},
	{"rustc", "rustc", []string{"--version"}},
	{"cargo", "cargo", []string{"--version"}},
	{"java", "java", []string{"-version"}},
	{"dotnet", "dotnet", []string{"--version"}},
	{"gcc", "gcc", []string{"--version"}},
	{"clang", "clang", []string{"--version"}},
	{"pwsh", "pwsh", []string{"--version"}},
	{"powershell", "powershell.exe", []string{"-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()"}},
}

// Runtimes reports the language runtimes and compilers this machine can execute,
// so a script can be written in a language that is actually present.
func Runtimes() []RuntimeView {
	out := make([]RuntimeView, 0, len(runtimeProbes))
	seen := map[string]bool{}
	for _, probe := range runtimeProbes {
		if seen[probe.name] {
			continue
		}
		path, err := exec.LookPath(probe.command)
		if err != nil {
			continue
		}
		seen[probe.name] = true
		view := RuntimeView{Name: probe.name, Command: probe.command, Path: path}
		if version, err := exec.Command(path, probe.args...).CombinedOutput(); err == nil {
			line := strings.TrimSpace(strings.SplitN(string(version), "\n", 2)[0])
			if len(line) > 80 {
				line = line[:80]
			}
			view.Version = line
		}
		out = append(out, view)
	}
	// DSH's bundled Python is usable even when nothing named python is on PATH.
	if !seen["python"] {
		if path := findBundledPython(); path != "python" {
			view := RuntimeView{Name: "python (DSH bundled)", Command: path, Path: path}
			if version, err := exec.Command(path, "--version").CombinedOutput(); err == nil {
				view.Version = strings.TrimSpace(strings.SplitN(string(version), "\n", 2)[0])
			}
			out = append(out, view)
		}
	}
	if runtime.GOOS == "windows" && !seen["powershell"] && !seen["pwsh"] {
		out = append(out, RuntimeView{Name: "powershell (5.1 expected)", Command: "powershell.exe"})
	}
	return out
}

// findBundledPython locates the Python runtime DSH ships under DSH_HOME.
func findBundledPython() string {
	home := os.Getenv("DSH_HOME")
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, ".dsh")
		}
	}
	if home == "" {
		return "python"
	}
	entries, err := os.ReadDir(filepath.Join(home, "dsh-runtimes"))
	if err != nil {
		return "python"
	}
	for _, entry := range entries {
		candidate := filepath.Join(home, "dsh-runtimes", entry.Name(), "dependencies", "python", "python.exe")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
	}
	return "python"
}
