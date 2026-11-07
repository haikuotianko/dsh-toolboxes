package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/haikuotianko/toolbox/internal/catalog"
	"github.com/haikuotianko/toolbox/internal/store"
)

// ToolDetail is everything the page shows for one installed tool.
type ToolDetail struct {
	Manifest *store.Manifest `json:"manifest,omitempty"`
	Readme   string          `json:"readme,omitempty"`
	Catalog  *catalog.Tool   `json:"catalog,omitempty"`
	Sources  []SourceView    `json:"sources,omitempty"`
}

// ToolDetail reads the manifest, its README, and the effective catalog entry
// (builtin merged with the user layer) for one tool.
func (s *Service) ToolDetail(id string) (ToolDetail, error) {
	if !store.ValidID(id) {
		return ToolDetail{}, fmt.Errorf("invalid tool id: %q", id)
	}
	out := ToolDetail{}
	if manifest, err := store.Load(s.root, id); err == nil && manifest != nil {
		out.Manifest = manifest
		readme, readErr := os.ReadFile(filepath.Join(store.ToolDir(s.root, id), "README.md"))
		if readErr == nil {
			out.Readme = strings.TrimRight(string(readme), "\n")
		}
	}
	cat, err := catalog.Load(s.root)
	if err != nil {
		return out, err
	}
	if entry, ok := cat.Find(id); ok {
		out.Catalog = &entry
		for _, source := range entry.SourcesFor(runtime.GOOS, runtime.GOARCH) {
			out.Sources = append(out.Sources, SourceView{URL: source.URL, Archive: source.Archive})
		}
	}
	return out, nil
}

// SaveSources replaces the download sources of one tool in the user catalog and
// returns the effective entry afterwards.
func (s *Service) SaveSources(id string, sources []catalog.Source) (catalog.Tool, error) {
	if !store.ValidID(id) {
		return catalog.Tool{}, fmt.Errorf("invalid tool id: %q", id)
	}
	cat, err := catalog.Load(s.root)
	if err != nil {
		return catalog.Tool{}, err
	}
	entry, ok := cat.Find(id)
	if !ok {
		entry = catalog.Tool{ID: id, Title: id, Binaries: []string{id}}
	}
	cleaned := make([]catalog.Source, 0, len(sources))
	for _, source := range sources {
		url := strings.TrimSpace(source.URL)
		if url == "" {
			continue
		}
		cleaned = append(cleaned, catalog.Source{
			OS: source.OS, Arch: source.Arch, URL: url,
			Archive: strings.TrimSpace(source.Archive), Single: source.Single,
		})
	}
	entry.Sources = cleaned
	if err := catalog.SaveUserEntry(s.root, entry); err != nil {
		return catalog.Tool{}, err
	}
	return entry, nil
}
