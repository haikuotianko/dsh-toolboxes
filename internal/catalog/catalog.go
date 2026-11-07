// Package catalog loads the built-in tool catalog and merges a user override
// file (<root>/catalog.json) on top of it, keyed by tool id.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed catalog.json
var builtinJSON []byte

// Source is one downloadable origin for a tool.
type Source struct {
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
	URL     string `json:"url"`
	Archive string `json:"archive,omitempty"`
	Single  bool   `json:"single,omitempty"`
	// SHA256, when set, is the hex digest the downloaded file must match before
	// anything gets extracted or executed. Leave it empty for rolling URLs
	// (a "latest" release) whose bytes change under the same name.
	SHA256 string `json:"sha256,omitempty"`
}

// Tool is one catalog entry.
type Tool struct {
	ID          string   `json:"id"`
	Title       string   `json:"title,omitempty"`
	Category    string   `json:"category,omitempty"`
	Description string   `json:"description,omitempty"`
	Usage       string   `json:"usage,omitempty"`
	Binaries    []string `json:"binaries,omitempty"`
	Single      bool     `json:"single,omitempty"`
	Sources     []Source `json:"sources,omitempty"`
}

// Catalog is the whole file.
type Catalog struct {
	Version int    `json:"version"`
	Note    string `json:"note,omitempty"`
	Tools   []Tool `json:"tools"`
}

// Builtin parses the embedded catalog.
func Builtin() (Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(builtinJSON, &c); err != nil {
		return Catalog{}, fmt.Errorf("parse builtin catalog: %w", err)
	}
	return c, nil
}

// Load returns the builtin catalog with <root>/catalog.json entries applied.
// A user entry with an existing id replaces that tool completely; a new id is
// appended, so users can add their own tools without touching the binary.
func Load(root string) (Catalog, error) {
	base, err := Builtin()
	if err != nil {
		return Catalog{}, err
	}
	if root == "" {
		return base, nil
	}
	raw, err := os.ReadFile(filepath.Join(root, "catalog.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return Catalog{}, fmt.Errorf("read user catalog: %w", err)
	}
	var user Catalog
	if err := json.Unmarshal(raw, &user); err != nil {
		return Catalog{}, fmt.Errorf("parse user catalog: %w", err)
	}
	index := make(map[string]int, len(base.Tools))
	for i, tool := range base.Tools {
		index[tool.ID] = i
	}
	for _, tool := range user.Tools {
		if strings.TrimSpace(tool.ID) == "" {
			continue
		}
		if i, ok := index[tool.ID]; ok {
			base.Tools[i] = tool
			continue
		}
		index[tool.ID] = len(base.Tools)
		base.Tools = append(base.Tools, tool)
	}
	return base, nil
}

// Find looks up one tool by id.
func (c Catalog) Find(id string) (Tool, bool) {
	for _, tool := range c.Tools {
		if tool.ID == id {
			return tool, true
		}
	}
	return Tool{}, false
}

// SaveUserEntry writes one tool into <root>/catalog.json, replacing a same-id
// entry (the user layer always wins over the embedded catalog) or appending it.
func SaveUserEntry(root string, entry Tool) error {
	path := filepath.Join(root, "catalog.json")
	user := Catalog{Version: 1}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &user)
	}
	replaced := false
	for i := range user.Tools {
		if user.Tools[i].ID == entry.ID {
			user.Tools[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		user.Tools = append(user.Tools, entry)
	}
	if user.Version == 0 {
		user.Version = 1
	}
	raw, err := json.MarshalIndent(user, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// SourcesFor keeps the sources that match the running OS and architecture, in
// catalog order — that order is the failover order.
func (t Tool) SourcesFor(goos, goarch string) []Source {
	out := make([]Source, 0, len(t.Sources))
	for _, source := range t.Sources {
		if source.URL == "" {
			continue
		}
		if source.OS != "" && !strings.EqualFold(source.OS, goos) {
			continue
		}
		if source.Arch != "" && !strings.EqualFold(source.Arch, goarch) {
			continue
		}
		out = append(out, source)
	}
	return out
}
