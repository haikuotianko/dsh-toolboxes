package httpapi

import (
	"net/http"
	"os"
)

// launcherConfig is the MCP row for this installation. The plugin page gets the
// same shape from its Node half; deriving it from the running binary means the
// command path is always real, whichever process serves the page.
type launcherConfig struct {
	Command string           `json:"command"`
	Root    string           `json:"root"`
	Args    []string         `json:"args"`
	Cordis  map[string]any   `json:"cordis"`
	JSON    map[string]any   `json:"json"`
}

func (s *Server) handleLauncher(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	command, err := os.Executable()
	if err != nil || command == "" {
		command = "toolbox"
	}
	root := s.svc.Root()
	args := []string{"-mcp", "-root", root}
	writeJSON(w, http.StatusOK, launcherConfig{
		Command: command,
		Root:    root,
		Args:    args,
		Cordis: map[string]any{
			"insert": []any{map[string]any{
				"id":   "dsh-toolbox-mcp",
				"name": "@deepseek-ai/dsh-mcp-client",
				"config": map[string]any{
					"transport":          "stdio",
					"serverName":         "toolbox",
					"command":            command,
					"args":               args,
					"toolCallTimeoutMs":  600000,
					"failOnStartupError": true,
				},
			}},
		},
		JSON: map[string]any{
			"mcpServers": map[string]any{
				"toolbox": map[string]any{"command": command, "args": args},
			},
		},
	})
}
