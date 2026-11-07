package mcpserver

import (
	"os"

	"github.com/haikuotianko/dsh-toolboxes/internal/install"
	"github.com/haikuotianko/dsh-toolboxes/internal/store"
)

// ScriptView is one archived script with its source, for the manager page.
type ScriptView struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Usage       string `json:"usage,omitempty"`
	Language    string `json:"language,omitempty"`
	Content     string `json:"content"`
	Path        string `json:"path,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// ListScripts returns every manifest archived as a script (kind=script).
func (s *Service) ListScripts() ([]ScriptView, error) {
	manifests, err := store.List(s.root)
	if err != nil {
		return nil, err
	}
	out := make([]ScriptView, 0)
	for _, manifest := range manifests {
		if manifest.Kind != "script" {
			continue
		}
		view := ScriptView{
			ID: manifest.ID, Title: manifest.Title, Description: manifest.Description,
			Usage: manifest.Usage, Language: manifest.Language, Path: manifest.Source,
			Enabled: store.IsEnabled(manifest),
		}
		if len(manifest.Args) > 0 {
			if raw, readErr := os.ReadFile(manifest.Args[0]); readErr == nil {
				view.Content = string(raw)
			}
		}
		out = append(out, view)
	}
	return out, nil
}

// SaveScriptFromPage creates or replaces a script from the page editor.
func (s *Service) SaveScriptFromPage(spec install.ScriptSpec) (ScriptView, error) {
	manifest, err := install.SaveScript(s.root, spec)
	if err != nil {
		return ScriptView{}, err
	}
	return ScriptView{
		ID: manifest.ID, Title: manifest.Title, Description: manifest.Description,
		Usage: manifest.Usage, Language: manifest.Language, Content: spec.Content,
		Path: manifest.Source, Enabled: true,
	}, nil
}

// DeleteScript removes a script and its file.
func (s *Service) DeleteScript(id string) error { return store.Remove(s.root, id) }
