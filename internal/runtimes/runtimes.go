// Package runtimes remembers which language runtimes this machine actually has,
// and at what version, so an agent writes a script for the interpreter that is
// installed instead of guessing (PowerShell 5.1 does not understand the syntax
// of 7, and a machine may have neither py nor python on PATH).
//
// Probing spawns processes, and the MCP server and the manager page are
// different processes, so the result is cached in <root>/runtimes.json:
//
//	{"checkedAt":"…","host":"windows-amd64","entries":[…]}
//
// Callers ask for a snapshot; it is re-probed only when the cached one is older
// than the TTL, missing, or explicitly refreshed.
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

// probeTimeout bounds one version call: a broken interpreter must not hang the
// whole refresh.
const probeTimeout = 8 * time.Second

// Entry is one runtime this machine can execute.
type Entry struct {
	ID      string `json:"id"`              // python | node | powershell | rustc | custom id
	Name    string `json:"name"`            // display name
	Kind    string `json:"kind"`            // interpreter | compiler | shell
	Command string `json:"command"`         // what to execute (bare name or path)
	Path    string `json:"path,omitempty"`  // resolved absolute path
	Version string `json:"version,omitempty"`
	Raw     string `json:"raw,omitempty"`   // first line of --version output
	Source  string `json:"source,omitempty"` // path | dsh-bundled | custom
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
	Entries   []Entry   `json:"entries"`
}

// Scriptable reports whether a saved script can run on this runtime.
func (e Entry) Scriptable() bool { return e.Found && e.Ext != "" }

type target struct {
	id          string
	name        string
	kind        string
	commands    []string
	versionArgs []string
	ext         string
}

// targets is the probe order: the first command that resolves wins.
var targets = []target{
	{id: "python", name: "Python", kind: "interpreter", commands: []string{"python", "python3", "py"}, versionArgs: []string{"--version"}, ext: "py"},
	{id: "node", name: "Node.js", kind: "interpreter", commands: []string{"node"}, versionArgs: []string{"--version"}, ext: "mjs"},
	{id: "deno", name: "Deno", kind: "interpreter", commands: []string{"deno"}, versionArgs: []string{"--version"}, ext: "mjs"},
	{id: "bun", name: "Bun", kind: "interpreter", commands: []string{"bun"}, versionArgs: []string{"--version"}, ext: "mjs"},
	{id: "ruby", name: "Ruby", kind: "interpreter", commands: []string{"ruby"}, versionArgs: []string{"--version"}, ext: "rb"},
	{id: "php", name: "PHP", kind: "interpreter", commands: []string{"php"}, versionArgs: []string{"--version"}, ext: "php"},
	{id: "pwsh", name: "PowerShell 7", kind: "shell", commands: []string{"pwsh"}, versionArgs: []string{"--version"}, ext: "ps1"},
	{id: "powershell", name: "Windows PowerShell", kind: "shell", commands: []string{"powershell.exe", "powershell"}, versionArgs: []string{"-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()"}, ext: "ps1"},
	{id: "go", name: "Go", kind: "compiler", commands: []string{"go"}, versionArgs: []string{"version"}},
	{id: "rustc", name: "Rust", kind: "compiler", commands: []string{"rustc"}, versionArgs: []string{"--version"}},
	{id: "cargo", name: "Cargo", kind: "compiler", commands: []string{"cargo"}, versionArgs: []string{"--version"}},
	{id: "dotnet", name: ".NET", kind: "compiler", commands: []string{"dotnet"}, versionArgs: []string{"--version"}},
	{id: "java", name: "Java", kind: "compiler", commands: []string{"java"}, versionArgs: []string{"-version"}},
	{id: "gcc", name: "GCC", kind: "compiler", commands: []string{"gcc"}, versionArgs: []string{"--version"}},
	{id: "clang", name: "Clang", kind: "compiler", commands: []string{"clang"}, versionArgs: []string{"--version"}},
}

var versionPattern = regexp.MustCompile(`\d+(?:\.\d+)+|\d+`)

// Get returns the cached snapshot, probing first when it is missing, older than
// ttl, or refresh is set. cached reports whether the file was used as is.
func Get(ctx context.Context, root string, ttl time.Duration, refresh bool) (Snapshot, bool, error) {
	if !refresh {
		if snapshot, err := Load(root); err == nil && snapshot != nil {
			age := time.Since(snapshot.CheckedAt)
			if age >= 0 && age < ttl {
				return *snapshot, true, nil
			}
		}
	}
	snapshot, err := Refresh(ctx, root)
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

// Refresh probes every known runtime, keeps user-added entries, and saves.
func Refresh(ctx context.Context, root string) (Snapshot, error) {
	custom := customEntries(root)
	snapshot := Snapshot{CheckedAt: time.Now(), Host: runtime.GOOS + "-" + runtime.GOARCH}

	for _, t := range targets {
		if entry, ok := probe(ctx, t); ok {
			snapshot.Entries = append(snapshot.Entries, entry)
		}
	}
	// DSH ships its own Python; it is usable even with nothing on PATH.
	if !hasID(snapshot.Entries, "python") {
		if path := bundledPython(); path != "" {
			entry := Entry{ID: "python", Name: "Python (DSH bundled)", Kind: "interpreter", Command: path, Path: path, Source: "dsh-bundled", Found: true, Ext: "py"}
			entry.Version, entry.Raw = probeVersion(ctx, path, []string{"--version"})
			entry.Notes = notes(entry)
			snapshot.Entries = append(snapshot.Entries, entry)
		}
	}
	for _, entry := range custom {
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

// AddCustom registers a language the built-in probe list does not know (rust,
// redshift, a portable python…). The command is probed once for its version.
func AddCustom(ctx context.Context, root, id, name, command string) (Snapshot, error) {
	id = strings.TrimSpace(strings.ToLower(id))
	command = strings.TrimSpace(command)
	if id == "" || !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`).MatchString(id) {
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
		// No cache yet: probe the builtins so the custom row lands in a real list.
		fresh, refreshErr := Refresh(ctx, root)
		if refreshErr != nil {
			return Snapshot{}, refreshErr
		}
		snapshot = &fresh
	}
	entry := Entry{ID: id, Name: firstNonEmpty(name, id), Kind: "custom", Command: command, Source: "custom", Found: false}
	if path, lookErr := exec.LookPath(command); lookErr == nil {
		entry.Path = path
		entry.Command = path
		entry.Found = true
		entry.Version, entry.Raw = probeVersion(ctx, path, []string{"--version"})
		if entry.Version == "" {
			entry.Version, entry.Raw = probeVersion(ctx, path, []string{"-version"})
		}
	} else if info, statErr := os.Stat(command); statErr == nil && !info.IsDir() {
		entry.Path = command
		entry.Found = true
		entry.Version, entry.Raw = probeVersion(ctx, command, []string{"--version"})
	} else {
		entry.Error = "找不到这个命令或文件"
	}
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

func probe(ctx context.Context, t target) (Entry, bool) {
	for _, candidate := range t.commands {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		entry := Entry{
			ID: t.id, Name: t.name, Kind: t.kind, Command: candidate,
			Path: path, Source: "path", Found: true, Ext: t.ext,
		}
		entry.Version, entry.Raw = probeVersion(ctx, path, t.versionArgs)
		entry.Notes = notes(entry)
		return entry, true
	}
	return Entry{}, false
}

// probeVersion runs one version command and reduces it to a version token plus
// the raw first line (PowerShell prints to stdout, java to stderr — both ok).
func probeVersion(ctx context.Context, command string, args []string) (string, string) {
	runCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(runCtx, command, args...).CombinedOutput()
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
	if match := versionPattern.FindString(line); match != "" {
		return match, line
	}
	if err != nil {
		return "", line
	}
	return "", line
}

// notes records the traps that make a model write the wrong dialect.
func notes(entry Entry) string {
	switch entry.ID {
	case "powershell":
		major := majorOf(entry.Version)
		if major > 0 && major < 7 {
			return "Windows PowerShell 5.1：不支持 ?: 三元、?? 空合并、&& / || 管道链、ForEach-Object -Parallel、写 `-Encoding utf8` 也会产出 BOM；脚本请按 5.1 语法写"
		}
		return "Windows PowerShell"
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

func majorOf(version string) int {
	return leadingInt(version)
}

func minorOf(version string) int {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0
	}
	return leadingInt(parts[1])
}

func leadingInt(text string) int {
	value := 0
	seen := false
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
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
	}
	return ""
}

// customEntries re-reads the previous snapshot and keeps the user's own rows so
// a refresh does not wipe them.
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
		// The command may have moved: re-resolve rather than trusting the cache.
		if path, lookErr := exec.LookPath(entry.Command); lookErr == nil {
			entry.Path = path
			entry.Command = path
			entry.Found = true
			entry.Error = ""
		} else if _, statErr := os.Stat(entry.Command); statErr == nil {
			entry.Found = true
			entry.Error = ""
		} else {
			entry.Found = false
			entry.Error = "找不到这个命令或文件"
		}
		out = append(out, entry)
	}
	return out
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
