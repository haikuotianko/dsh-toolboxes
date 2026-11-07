// Package httpapi serves the MCP endpoint plus a small JSON API over loopback so
// a GUI (the DSH plugin page, or a browser) can drive the same operations.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/haikuotianko/dsh-toolboxes/internal/catalog"
	"github.com/haikuotianko/dsh-toolboxes/internal/install"
	"github.com/haikuotianko/dsh-toolboxes/internal/mcpserver"
)

// Server wires the MCP handler and the JSON API to one service.
type Server struct {
	svc *mcpserver.Service
	mcp http.Handler
	// UI is the embedded manager page, served at "/" when non-empty.
	UI []byte
}

// New builds the handler set.
func New(svc *mcpserver.Service) *Server {
	return &Server{
		svc: svc,
		mcp: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return svc.NewServer() }, nil),
	}
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", s.mcp)
	mux.Handle("/mcp/", s.mcp)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/install", s.handleInstall)
	mux.HandleFunc("/api/job", s.handleJob)
	mux.HandleFunc("/api/cancel", s.handleCancel)
	mux.HandleFunc("/api/sources", s.handleSources)
	mux.HandleFunc("/api/tool", s.handleTool)
	mux.HandleFunc("/api/source", s.handleSaveSource)
	mux.HandleFunc("/api/import", s.handleImport)
	mux.HandleFunc("/api/env", s.handleEnv)
	mux.HandleFunc("/api/runtimes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"runtimes": mcpserver.Runtimes()})
	})
	mux.HandleFunc("/api/adopt", s.handleAdopt)
	mux.HandleFunc("/api/enable", s.handleEnable)
	mux.HandleFunc("/api/remove", s.handleRemove)
	s.registerScriptRoutes(mux)
	s.registerRunRoutes(mux)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/run", s.handleRun)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if len(s.UI) > 0 && (r.URL.Path == "/" || r.URL.Path == "/index.html") {
			w.Header().Set("content-type", "text/html; charset=utf-8")
			w.Header().Set("cache-control", "no-store")
			_, _ = w.Write(s.UI)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not-found"})
	})
	return mux
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	state, err := s.svc.State()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// handleInstall starts a background install and returns its job id. Pass
// ?wait=1 for the old blocking behaviour (used by scripted callers).
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		Tool    string `json:"tool"`
		URL     string `json:"url"`
		Archive string `json:"archive"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	job, err := s.svc.StartInstall(body.Tool, body.URL, body.Archive)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if r.URL.Query().Get("wait") == "1" {
		view, waitErr := s.svc.WaitInstall(r.Context(), job)
		if waitErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": waitErr.Error(), "job": view})
			return
		}
		writeJSON(w, http.StatusOK, view.Result)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "tool": body.Tool})
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	id := r.URL.Query().Get("id")
	view, ok := s.svc.JobSnapshot(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown-job"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		Job string `json:"job"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": s.svc.CancelInstall(body.Job)})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		Tool      string   `json:"tool"`
		Args      []string `json:"args"`
		Cwd       string   `json:"cwd"`
		TimeoutMs int      `json:"timeout_ms"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	out, err := s.svc.Run(r.Context(), body.Tool, body.Args, body.Cwd, body.TimeoutMs, mcpserver.OriginPage)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUpload accepts a raw binary the user dropped in the manager page.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	tool := strings.TrimSpace(r.URL.Query().Get("tool"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if tool == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tool query parameter is required"})
		return
	}
	if name == "" {
		name = tool
	}
	temp, err := os.CreateTemp("", "toolbox-upload-*"+filepath.Ext(name))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	written, err := io.Copy(temp, io.LimitReader(r.Body, 512<<20))
	temp.Close()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if written == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "empty file"})
		return
	}
	out, err := s.svc.ImportPath(r.Context(), tool, tempPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRemove uninstalls a tool: downloaded ones lose their files, adopted or
// imported ones only lose the record.
func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		Tool        string `json:"tool"`
		RemoveFiles *bool  `json:"removeFiles"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	filesRemoved, err := s.svc.RemoveTool(body.Tool, body.RemoveFiles)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": body.Tool, "filesRemoved": filesRemoved})
}

// handleTool returns the manifest, README and effective sources of one tool.
func (s *Server) handleTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	detail, err := s.svc.ToolDetail(r.URL.Query().Get("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleSaveSource writes the user's own source list for one tool.
func (s *Server) handleSaveSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		Tool    string `json:"tool"`
		Sources []struct {
			URL     string `json:"url"`
			Archive string `json:"archive"`
			OS      string `json:"os"`
			Arch    string `json:"arch"`
			Single  bool   `json:"single"`
			SHA256  string `json:"sha256"`
		} `json:"sources"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	sources := make([]catalog.Source, 0, len(body.Sources))
	for _, source := range body.Sources {
		sources = append(sources, catalog.Source{URL: source.URL, Archive: source.Archive, OS: source.OS, Arch: source.Arch, Single: source.Single, SHA256: source.SHA256})
	}
	entry, err := s.svc.SaveSources(body.Tool, sources)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool": entry.ID, "sources": entry.Sources})
}

// handleImport registers a local executable path the user typed.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		Tool string `json:"tool"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	out, err := s.svc.ImportPath(r.Context(), body.Tool, body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleEnv reports which catalog tools already exist on this machine's PATH.
func (s *Server) handleEnv(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	hits, err := s.svc.ScanEnvironment()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
}

// handleAdopt registers a local binary in place instead of downloading it.
func (s *Server) handleAdopt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		Tool string `json:"tool"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	manifest, err := s.svc.AdoptSystem(body.Tool, body.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool": manifest.ID, "path": manifest.Command, "version": manifest.Version})
}

// handleEnable flips an installed tool's enable flag.
func (s *Server) handleEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		Tool    string `json:"tool"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	manifest, err := s.svc.SetEnabled(body.Tool, body.Enabled)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool": manifest.ID, "enabled": body.Enabled})
}

// handleSources measures every catalog source for one tool (512KB window each)
// so the page can show real download speeds before the user commits.
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	tool := r.URL.Query().Get("tool")
	cat, err := catalog.Load(s.svc.Root())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	entry, ok := cat.Find(tool)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown tool: " + tool})
		return
	}
	sources := entry.SourcesFor(runtime.GOOS, runtime.GOARCH)
	if single := r.URL.Query().Get("url"); single != "" {
		sources = []catalog.Source{{URL: single}}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool": tool, "sources": install.ProbeSources(r.Context(), sources)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(status)
	body, err := json.Marshal(value)
	if err != nil {
		_, _ = w.Write([]byte(`{"error":"encode"}`))
		return
	}
	_, _ = w.Write(body)
}

var _ = context.Background
var _ = fmt.Sprintf