// Package store owns the on-disk layout of the toolbox root:
//
//	<root>/catalog.json          optional user catalog override
//	<root>/tools/<id>/tool.json  one manifest per installed tool
//	<root>/tools/<id>/bin/       the executable(s)
//	<root>/tools/<id>/README.md  what it is and how to call it
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Manifest describes one installed tool.
type Manifest struct {
	ID          string    `json:"id"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	Usage       string    `json:"usage,omitempty"`
	Kind        string    `json:"kind,omitempty"` // binary | script
	Language    string    `json:"language,omitempty"`
	Binaries    []string  `json:"binaries,omitempty"`
	Command     string    `json:"command"`
	Args        []string  `json:"args,omitempty"`
	Version     string    `json:"version,omitempty"`
	Source      string    `json:"source,omitempty"`
	SizeBytes   int64     `json:"sizeBytes,omitempty"`
	Enabled     *bool     `json:"enabled,omitempty"`
	InstalledAt time.Time `json:"installedAt"`
}

// IsEnabled reports the effective enable flag (absent means enabled).
func IsEnabled(m *Manifest) bool {
	if m == nil || m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// ValidID reports whether id is safe to use as a directory name.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// ToolsDir is <root>/tools.
func ToolsDir(root string) string { return filepath.Join(root, "tools") }

// ToolDir is <root>/tools/<id>.
func ToolDir(root, id string) string { return filepath.Join(ToolsDir(root), id) }

// BinDir is <root>/tools/<id>/bin.
func BinDir(root, id string) string { return filepath.Join(ToolDir(root, id), "bin") }

// Load reads one manifest; a missing tool returns (nil, nil).
func Load(root, id string) (*Manifest, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("invalid tool id: %q", id)
	}
	raw, err := os.ReadFile(filepath.Join(ToolDir(root, id), "tool.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest for %s: %w", id, err)
	}
	return &m, nil
}

// Save writes the manifest, creating the tool directory.
func Save(root string, m *Manifest) error {
	if !ValidID(m.ID) {
		return fmt.Errorf("invalid tool id: %q", m.ID)
	}
	if m.InstalledAt.IsZero() {
		m.InstalledAt = time.Now()
	}
	dir := ToolDir(root, m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tool.json"), append(raw, '\n'), 0o644)
}

// List returns every installed manifest, sorted by id.
func List(root string) ([]*Manifest, error) {
	entries, err := os.ReadDir(ToolsDir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		m, err := Load(root, entry.Name())
		if err != nil || m == nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Remove deletes a tool directory.
func Remove(root, id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid tool id: %q", id)
	}
	return os.RemoveAll(ToolDir(root, id))
}

// WriteReadme writes the human/model readable usage note next to the manifest.
func WriteReadme(root string, m *Manifest) error {
	lines := []string{
		"# " + firstNonEmpty(m.Title, m.ID) + " (" + m.ID + ")",
		"",
		m.Description,
		"",
		"## 用法",
		"",
		firstNonEmpty(m.Usage, `toolbox_run { tool: "`+m.ID+`", args: ["--help"] }`),
		"",
		"## 运行",
		"",
		"command: " + m.Command,
	}
	if len(m.Binaries) > 0 {
		lines = append(lines, "binaries: "+strings.Join(m.Binaries, ", "))
	}
	if m.Version != "" {
		lines = append(lines, "version: "+m.Version)
	}
	if m.Source != "" {
		lines = append(lines, "source: "+m.Source)
	}
	body := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(filepath.Join(ToolDir(root, m.ID), "README.md"), []byte(body), 0o644)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
