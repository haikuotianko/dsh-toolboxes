package mcpserver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/haikuotianko/toolbox/internal/catalog"
	"github.com/haikuotianko/toolbox/internal/store"
)

// EnvHit is one catalog tool that already exists on this machine.
type EnvHit struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Usage       string `json:"usage,omitempty"`
	Binary      string `json:"binary"`
	Path        string `json:"path"`
	Managed     bool   `json:"managed"` // already registered in the toolbox
}

// ScanEnvironment looks up every catalog binary on PATH (plus the toolbox's own
// bin dir) so the page can mark what this machine already has.
func (s *Service) ScanEnvironment() ([]EnvHit, error) {
	cat, err := catalog.Load(s.root)
	if err != nil {
		return nil, err
	}
	installed := map[string]bool{}
	if manifests, err := store.List(s.root); err == nil {
		for _, manifest := range manifests {
			installed[manifest.ID] = true
		}
	}
	hits := make([]EnvHit, 0, len(cat.Tools))
	for _, tool := range cat.Tools {
		names := tool.Binaries
		if len(names) == 0 {
			names = []string{tool.ID}
		}
		for _, name := range names {
			candidate := name
			if runtime.GOOS == "windows" {
				candidate += ".exe"
			}
			if path, err := exec.LookPath(candidate); err == nil {
				absolute, absErr := filepath.Abs(path)
				if absErr == nil {
					path = absolute
				}
				hits = append(hits, EnvHit{
					ID:          tool.ID,
					Title:       tool.Title,
					Description: tool.Description,
					Usage:       tool.Usage,
					Binary:      name,
					Path:        path,
					Managed:     installed[tool.ID],
				})
				break
			}
		}
	}
	return hits, nil
}

// AdoptSystem registers an already-installed binary in place (no copy), so the
// agent can call it through toolbox_run without downloading anything.
func (s *Service) AdoptSystem(id, path string) (store.Manifest, error) {
	if !store.ValidID(id) {
		return store.Manifest{}, fmt.Errorf("invalid tool id: %q", id)
	}
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return store.Manifest{}, err
	}
	if _, err := exec.LookPath(absolute); err != nil {
		return store.Manifest{}, fmt.Errorf("不能执行 %s: %w", absolute, err)
	}
	cat, _ := catalog.Load(s.root)
	entry, _ := cat.Find(id)
	name := strings.TrimSuffix(filepath.Base(absolute), ".exe")
	manifest := store.Manifest{
		ID:          id,
		Title:       firstNonEmpty(entry.Title, id),
		Description: firstNonEmpty(entry.Description, "系统已安装的程序（未复制到工具箱）"),
		Usage:       firstNonEmpty(entry.Usage, `toolbox_run { tool: "`+id+`", args: ["--help"] }`),
		Kind:        "binary",
		Binaries:    []string{name},
		Command:     absolute,
		Version:     probeBinary(absolute),
		Source:      "system:" + absolute,
	}
	if err := store.Save(s.root, &manifest); err != nil {
		return store.Manifest{}, err
	}
	_ = store.WriteReadme(s.root, &manifest)
	return manifest, nil
}

// RemoveTool uninstalls a tool. Files are deleted only for tools the toolbox
// itself downloaded; a system-adopted or user-imported binary keeps its files
// and only loses its record, so a local program is never destroyed.
func (s *Service) RemoveTool(id string, removeFiles *bool) (bool, error) {
	manifest, err := store.Load(s.root, id)
	if err != nil {
		return false, err
	}
	if manifest == nil {
		return false, fmt.Errorf("tool %q is not installed", id)
	}
	remove := strings.HasPrefix(manifest.Source, "http://") || strings.HasPrefix(manifest.Source, "https://")
	if removeFiles != nil {
		remove = *removeFiles
	}
	if remove {
		return true, store.Remove(s.root, id)
	}
	dir := store.ToolDir(s.root, id)
	_ = os.Remove(filepath.Join(dir, "tool.json"))
	_ = os.Remove(filepath.Join(dir, "README.md"))
	return false, nil
}

// SetEnabled flips the enable flag of an installed tool.
func (s *Service) SetEnabled(id string, enabled bool) (store.Manifest, error) {
	manifest, err := store.Load(s.root, id)
	if err != nil {
		return store.Manifest{}, err
	}
	if manifest == nil {
		return store.Manifest{}, fmt.Errorf("tool %q is not installed", id)
	}
	value := enabled
	manifest.Enabled = &value
	if err := store.Save(s.root, manifest); err != nil {
		return store.Manifest{}, err
	}
	return *manifest, nil
}

func probeBinary(path string) string {
	for _, flag := range []string{"-version", "--version", "-v"} {
		out, err := exec.Command(path, flag).CombinedOutput()
		if err != nil {
			continue
		}
		line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if line != "" {
			if len(line) > 120 {
				line = line[:120]
			}
			return line
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
