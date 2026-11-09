// Package runtimes remembers which language runtimes this machine actually has,
// and at what version, so an agent writes a script for the interpreter that is
// installed instead of guessing (PowerShell 5.1 does not understand the syntax
// of 7, and a machine may have neither py nor python on PATH).
//
// Discovery is environment-driven and side-effect free: it reads PATH and the
// usual *_HOME / *_ROOT variables and stats those directories. It never spawns
// an interpreter by default — `--version` is only run when the caller asks for
// exact versions (exec), because starting a process per language is slow and,
// on Windows, flashes a console window each time.
//
// The result is cached in <root>/runtimes.json, shared by the MCP server and the
// manager page (different processes), so a new window does not re-probe.
package runtimes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// FileName is the snapshot file inside the toolbox root.
const FileName = "runtimes.json"

// DefaultTTL is how long a snapshot is trusted before a re-probe.
const DefaultTTL = 12 * time.Hour

// probeTimeout bounds one version call in exec mode.
const probeTimeout = 8 * time.Second

// How a snapshot was produced.
const (
	ModeEnv  = "env"  // environment only: PATH + *_HOME, no process started
	ModeExec = "exec" // additionally ran `<cmd> --version` for exact versions
)

// Entry is one runtime this machine can execute.
type Entry struct {
	ID      string `json:"id"`               // python | node | powershell | rustc | custom id
	Name    string `json:"name"`             // display name
	Kind    string `json:"kind"`             // interpreter | compiler | shell
	Command string `json:"command"`          // what to execute (bare name or path)
	Path    string `json:"path,omitempty"`   // resolved absolute path
	Version string `json:"version,omitempty"`
	Raw     string `json:"raw,omitempty"`    // first line of --version output (exec mode)
	Source  string `json:"source,omitempty"` // env:PATH | env:JAVA_HOME | dsh-bundled | custom
	Found   bool   `json:"found"`
	Error   string `json:"error,omitempty"`
	// Ext is the script extension this runtime can host ("" = not scriptable,
	// e.g. rustc: you compile, not run a file).
	Ext   string `json:"ext,omitempty"`
	Notes string `json:"notes,omitempty"` // gotchas worth telling the model
}

// Snapshot is the cached probe result.
type Snapshot struct {
	CheckedAt time.Time `json:"checkedAt"`
	Host      string    `json:"host"`
	// Mode is env or exec: it tells the reader whether Version is exact.
	Mode    string  `json:"mode,omitempty"`
	Entries []Entry `json:"entries"`
}

// Scriptable reports whether a saved script can run on this runtime.
func (e Entry) Scriptable() bool { return e.Found && e.Ext != "" }

type envDir struct {
	name string // environment variable holding a prefix
	sub  string // suffix appended to it
}

type target struct {
	id          string
	name        string
	kind        string
	commands    []string
	versionArgs []string
	ext         string
	// envDirs are extra install roots advertised by the environment, tried when
	// PATH has no match (JAVA_HOME/bin, GOROOT/bin, …).
	envDirs []envDir
	// versionEnv carry a version string, often in CI images.
	versionEnv []string
	// fixedVersion is what the product always reports (Windows PowerShell is 5.1
	// on every Windows since 2016); used when no number is derivable.
	fixedVersion string
}

// targets is the probe order: the first command that resolves wins.
var targets = []target{
	{id: "python", name: "Python", kind: "interpreter", commands: []string{"python", "python3", "py"}, versionArgs: []string{"--version"}, ext: "py", versionEnv: []string{"PYENV_VERSION", "PYTHON_VERSION"}},
	{id: "node", name: "Node.js", kind: "interpreter", commands: []string{"node"}, versionArgs: []string{"--version"}, ext: "mjs", versionEnv: []string{"NODE_VERSION", "NVM_CURRENT"}},
	{id: "deno", name: "Deno", kind: "interpreter", commands: []string{"deno"}, versionArgs: []string{"--version"}, ext: "mjs", envDirs: []envDir{{"DENO_INSTALL", "bin"}}},
	{id: "bun", name: "Bun", kind: "interpreter", commands: []string{"bun"}, versionArgs: []string{"--version"}, ext: "mjs", envDirs: []envDir{{"BUN_INSTALL", "bin"}}},
	{id: "ruby", name: "Ruby", kind: "interpreter", commands: []string{"ruby"}, versionArgs: []string{"--version"}, ext: "rb", versionEnv: []string{"RBENV_VERSION"}},
	{id: "php", name: "PHP", kind: "interpreter", commands: []string{"php"}, versionArgs: []string{"--version"}, ext: "php", versionEnv: []string{"PHP_VERSION"}},
	{id: "pwsh", name: "PowerShell 7", kind: "shell", commands: []string{"pwsh"}, versionArgs: []string{"--version"}, ext: "ps1", fixedVersion: "7"},
	{id: "powershell", name: "Windows PowerShell", kind: "shell", commands: []string{"powershell"}, versionArgs: []string{"-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()"}, ext: "ps1", fixedVersion: "5.1"},
	{id: "go", name: "Go", kind: "compiler", commands: []string{"go"}, versionArgs: []string{"version"}, envDirs: []envDir{{"GOROOT", "bin"}}},
	{id: "rustc", name: "Rust", kind: "compiler", commands: []string{"rustc"}, versionArgs: []string{"--version"}, envDirs: []envDir{{"CARGO_HOME", "bin"}}, versionEnv: []string{"RUSTUP_TOOLCHAIN"}},
	{id: "cargo", name: "Cargo", kind: "compiler", commands: []string{"cargo"}, versionArgs: []string{"--version"}, envDirs: []envDir{{"CARGO_HOME", "bin"}}, versionEnv: []string{"RUSTUP_TOOLCHAIN"}},
	{id: "dotnet", name: ".NET", kind: "compiler", commands: []string{"dotnet"}, versionArgs: []string{"--version"}, envDirs: []envDir{{"DOTNET_ROOT", ""}}},
	{id: "java", name: "Java", kind: "compiler", commands: []string{"java"}, versionArgs: []string{"-version"}, envDirs: []envDir{{"JAVA_HOME", "bin"}}, versionEnv: []string{"JAVA_VERSION"}},
	{id: "gcc", name: "GCC", kind: "compiler", commands: []string{"gcc"}, versionArgs: []string{"--version"}},
	{id: "clang", name: "Clang", kind: "compiler", commands: []string{"clang"}, versionArgs: []string{"--version"}},
}

var (
	// versionPattern needs at least one dot, so `python3.exe` in a file name is
	// not mistaken for "version 3".
	versionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)
	// pathVersionPattern is stricter: a directory is only trusted as a version
	// when it has three parts, otherwise `WindowsPowerShell\v1.0` and
	// `M:\Go1.24` (a stale install dir) would report the wrong numbers.
	pathVersionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)
	idPattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
)

// Get returns the cached snapshot, re-discovering when it is missing, older
// than ttl, or refresh is set. exec switches to exact versions.
func Get(ctx context.Context, root string, ttl time.Duration, refresh, exact bool) (Snapshot, bool, error) {
	if !refresh {
		if snapshot, err := Load(root); err == nil && snapshot != nil {
			age := time.Since(snapshot.CheckedAt)
			if age >= 0 && age < ttl && (!exact || snapshot.Mode == ModeExec) {
				return *snapshot, true, nil
			}
		}
	}
	snapshot, err := Refresh(ctx, root, exact)
	if err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, false, nil
}

// Load reads the snapshot; a missing file returns (nil, nil).
func Load(root string) (*Snapshot, error) {
	raw, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("parse %s: %w", FileName, err)
	}
	return &snapshot, nil
}

// Refresh discovers runtimes from the environment, keeps user-added entries,
// and saves. With exec it also runs one version command per runtime.
func Refresh(ctx context.Context, root string, exact bool) (Snapshot, error) {
	custom := customEntries(root)
	mode := ModeEnv
	if exact {
		mode = ModeExec
	}
	snapshot := Snapshot{CheckedAt: time.Now(), Host: runtime.GOOS + "-" + runtime.GOARCH, Mode: mode}

	for _, t := range targets {
		entry, ok := discover(t)
		if !ok {
			continue
		}
		if exact {
			entry.Version, entry.Raw = probeVersion(ctx, entry.Path, t.versionArgs)
			if entry.Version == "" {
				entry.Version = entry.fallbackVersion(t)
			}
		}
		entry.Notes = notes(entry)
		snapshot.Entries = append(snapshot.Entries, entry)
	}
	if !hasID(snapshot.Entries, "python") {
		if path := bundledPython(); path != "" {
			entry := Entry{ID: "python", Name: "Python (DSH bundled)", Kind: "interpreter", Command: path, Path: path, Source: "dsh-bundled", Found: true, Ext: "py"}
			entry.Version = versionFromPath(path)
			if exact {
				entry.Version, entry.Raw = probeVersion(ctx, path, []string{"--version"})
			}
			entry.Notes = notes(entry)
			snapshot.Entries = append(snapshot.Entries, entry)
		}
	}
	for _, entry := range custom {
		if exact && entry.Found {
			entry.Version = firstNonEmpty(entry.Version, "")
		}
		entry.Notes = notes(entry)
		snapshot.Entries = append(snapshot.Entries, entry)
	}

	sort.SliceStable(snapshot.Entries, func(i, j int) bool {
		return rank(snapshot.Entries[i].ID) < rank(snapshot.Entries[j].ID)
	})
	if err := Save(root, snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

// discover finds one runtime without starting it: PATH first, then the
// environment-declared install roots (JAVA_HOME/bin, GOROOT/bin, …).
func discover(t target) (Entry, bool) {
	entry := Entry{ID: t.id, Name: t.name, Kind: t.kind, Ext: t.ext, Found: true}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, command := range t.commands {
			candidate := filepath.Join(dir, exeName(command))
			if isFile(candidate) {
				entry.Command = command
				entry.Path = candidate
				entry.Source = "env:PATH"
				entry.Version = entry.guessVersion(t, candidate)
				return entry, true
			}
		}
	}
	for _, root := range t.envDirs {
		base := strings.TrimSpace(os.Getenv(root.name))
		if base == "" {
			continue
		}
		for _, command := range t.commands {
			candidate := filepath.Join(base, root.sub, exeName(command))
			if isFile(candidate) {
				entry.Command = candidate
				entry.Path = candidate
				entry.Source = "env:" + root.name
				entry.Version = entry.guessVersion(t, candidate)
				return entry, true
			}
		}
	}
	return Entry{}, false
}

// guessVersion derives a version from the environment: an explicit *_VERSION
// variable first, then a version-looking directory in the install path, then a
// product constant (Windows PowerShell is 5.1 everywhere).
func (e Entry) guessVersion(t target, path string) string {
	for _, name := range t.versionEnv {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			if match := versionPattern.FindString(value); match != "" {
				return match
			}
		}
	}
	if version := versionFromPath(path); version != "" {
		return version
	}
	return t.fixedVersion
}

func (e Entry) fallbackVersion(t target) string {
	if e.Version != "" {
		return e.Version
	}
	return t.fixedVersion
}

// versionFromPath looks for a version-looking segment in the directory part of
// an install path (…/node-versions/v24.21.0/…, …/python-3.12.14/…). File names
// are excluded on purpose: python3.exe is not version 3.
func versionFromPath(path string) string {
	dir := filepath.Dir(path)
	if match := pathVersionPattern.FindString(dir); match != "" {
		return match
	}
	return ""
}

// Save writes the snapshot atomically.
func Save(root string, snapshot Snapshot) error {
	if snapshot.Entries == nil {
		snapshot.Entries = []Entry{}
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(root, FileName)
	temp := target + ".tmp"
	if err := os.WriteFile(temp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(temp, target)
}

// AddCustom registers a language the built-in list does not know. command may be
// a bare name resolved on PATH or an absolute path; ext lets it host scripts
// ("ts" → toolbox_save_script writes name.ts and runs `<command> name.ts`);
// version is optional because discovery no longer executes anything.
func AddCustom(ctx context.Context, root, id, name, command, ext, version string, exact bool) (Snapshot, error) {
	id = strings.TrimSpace(strings.ToLower(id))
	command = strings.TrimSpace(command)
	ext = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(ext)), ".")
	if !idPattern.MatchString(id) {
		return Snapshot{}, fmt.Errorf("语言 id 只能用字母、数字、点、横线（例如 rust）")
	}
	if command == "" {
		return Snapshot{}, fmt.Errorf("请填可执行文件路径或命令名")
	}
	snapshot, err := Load(root)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot == nil {
		fresh, refreshErr := Refresh(ctx, root, exact)
		if refreshErr != nil {
			return Snapshot{}, refreshErr
		}
		snapshot = &fresh
	}

	entry := Entry{ID: id, Name: firstNonEmpty(name, id), Kind: "custom", Command: command, Source: "custom", Ext: ext, Version: strings.TrimSpace(version)}
	switch {
	case isFile(command):
		entry.Path = command
		entry.Found = true
	case func() bool { path, err := exec.LookPath(command); return err == nil && isFile(path) }():
		path, _ := exec.LookPath(command)
		entry.Path = path
		entry.Command = path
		entry.Found = true
	default:
		entry.Error = "环境变量里找不到这个命令或文件（只会查 PATH 和 *_HOME，不会真的执行）"
	}
	if entry.Found && entry.Version == "" {
		entry.Version = versionFromPath(entry.Path)
	}
	if entry.Found && entry.Version == "" && exact {
		entry.Version, entry.Raw = probeVersion(ctx, entry.Path, []string{"--version"})
	}
	entry.Notes = notes(entry)

	replaced := false
	for i := range snapshot.Entries {
		if snapshot.Entries[i].ID == id {
			snapshot.Entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		snapshot.Entries = append(snapshot.Entries, entry)
	}
	if err := Save(root, *snapshot); err != nil {
		return Snapshot{}, err
	}
	return *snapshot, nil
}

// RemoveCustom deletes a user-added runtime; built-ins cannot be removed.
func RemoveCustom(root, id string) (Snapshot, error) {
	snapshot, err := Load(root)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot == nil {
		return Snapshot{}, fmt.Errorf("还没有检测记录")
	}
	kept := snapshot.Entries[:0]
	removed := false
	for _, entry := range snapshot.Entries {
		if entry.ID == id && entry.Source == "custom" {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return Snapshot{}, fmt.Errorf("%s 不是自定义语言", id)
	}
	snapshot.Entries = kept
	if err := Save(root, *snapshot); err != nil {
		return Snapshot{}, err
	}
	return *snapshot, nil
}

// InterpreterFor resolves a saved script's language to the interpreter this
// machine actually has, using the cached snapshot first (no probing) and PATH
// as the fallback. An empty result means "let the caller decide".
func InterpreterFor(root, language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if snapshot, err := Load(root); err == nil && snapshot != nil {
		for _, entry := range snapshot.Entries {
			if entry.ID != language || !entry.Found {
				continue
			}
			if entry.Path != "" {
				return entry.Path
			}
			if entry.Command != "" {
				return entry.Command
			}
		}
	}
	switch language {
	case "python":
		for _, name := range []string{"python", "python3", "py"} {
			if path, err := exec.LookPath(name); err == nil {
				return path
			}
		}
		if path := bundledPython(); path != "" {
			return path
		}
	case "powershell":
		if path, err := exec.LookPath("pwsh"); err == nil {
			return path
		}
		if path, err := exec.LookPath("powershell.exe"); err == nil {
			return path
		}
		return "powershell.exe"
	case "node", "deno", "bun", "ruby", "php":
		if path, err := exec.LookPath(language); err == nil {
			return path
		}
	}
	return ""
}

// ExtensionFor returns the script extension a runtime declared, so a saved
// script can be written as name.<ext> and run as `<command> name.<ext>`.
// Only rows that are found and declared an extension qualify.
func ExtensionFor(root, language string) string {
	snapshot, err := Load(root)
	if err != nil || snapshot == nil {
		return ""
	}
	for _, entry := range snapshot.Entries {
		if entry.ID == language && entry.Found && entry.Ext != "" {
			return strings.TrimPrefix(entry.Ext, ".")
		}
	}
	return ""
}

// probeVersion runs one version command. Only exec mode calls this.
func probeVersion(ctx context.Context, command string, args []string) (string, string) {
	if command == "" {
		return "", ""
	}
	runCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command, args...)
	hideWindow(cmd)
	out, _ := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "", ""
	}
	line := text
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = line[:index]
	}
	line = strings.TrimSpace(line)
	if len(line) > 120 {
		line = line[:120]
	}
	return versionPattern.FindString(line), line
}

// notes records the traps that make a model write the wrong dialect.
func notes(entry Entry) string {
	switch entry.ID {
	case "powershell":
		return "Windows PowerShell 5.1：不支持 ?: 三元、?? 空合并、&& / || 管道链、ForEach-Object -Parallel；脚本请按 5.1 语法写"
	case "pwsh":
		return "PowerShell 7：支持 5.1 语法以及 ?:、??、&& / ||"
	case "python":
		if major := majorOf(entry.Version); major == 2 {
			return "Python 2：print 是语句、字符串默认 bytes，不要用 f-string"
		}
		if major := majorOf(entry.Version); major == 3 && minorOf(entry.Version) < 8 {
			return "Python 3.8 以下：不要用海象运算符、match 语句、str.removeprefix"
		}
	case "node":
		if major := majorOf(entry.Version); major > 0 && major < 18 {
			return "Node 18 以下：没有内置 fetch / 顶层 await 受限"
		}
	case "rustc", "cargo":
		return "编译型：先用 toolbox_run 跑 rustc/cargo 构建，再运行产物；不适合直接归档成脚本"
	}
	return ""
}

func majorOf(version string) int { return leadingInt(version) }

func minorOf(version string) int {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0
	}
	return leadingInt(parts[1])
}

func leadingInt(text string) int {
	value, seen := 0, false
	for _, r := range text {
		if r < '0' || r > '9' {
			break
		}
		seen = true
		value = value*10 + int(r-'0')
	}
	if !seen {
		return 0
	}
	return value
}

// bundledPython finds the Python runtime DSH ships, rooted at DSH_HOME.
func bundledPython() string {
	home := os.Getenv("DSH_HOME")
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, ".dsh")
		}
	}
	if home == "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(home, "dsh-runtimes"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		candidate := filepath.Join(home, "dsh-runtimes", entry.Name(), "dependencies", "python", "python.exe")
		if isFile(candidate) {
			return candidate
		}
	}
	return ""
}

// customEntries re-reads the previous snapshot and keeps the user's own rows so
// a refresh does not wipe them. Resolution is environment-only: PATH and file
// stat, never execution.
func customEntries(root string) []Entry {
	snapshot, err := Load(root)
	if err != nil || snapshot == nil {
		return nil
	}
	var out []Entry
	for _, entry := range snapshot.Entries {
		if entry.Source != "custom" {
			continue
		}
		switch {
		case isFile(entry.Command):
			entry.Path = entry.Command
			entry.Found = true
			entry.Error = ""
		case func() bool { path, err := exec.LookPath(entry.Command); return err == nil && isFile(path) }():
			path, _ := exec.LookPath(entry.Command)
			entry.Path, entry.Found, entry.Error = path, true, ""
		default:
			entry.Found = false
			entry.Error = "环境变量里找不到这个命令或文件（只会查 PATH 和 *_HOME，不会真的执行）"
		}
		out = append(out, entry)
	}
	return out
}

func isFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// exeName adds .exe on Windows when the catalog name omits it.
func exeName(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func hasID(entries []Entry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func rank(id string) int {
	for i, entry := range targets {
		if entry.id == id {
			return i
		}
	}
	return len(targets) + 1
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
