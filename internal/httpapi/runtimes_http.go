package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
)

func (s *Server) registerRuntimeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/runtimes", s.handleRuntimes)
	mux.HandleFunc("/api/runtimes/remove", s.handleRuntimeRemove)
}

// handleRuntimes returns the language inventory. It answers from the cached
// snapshot unless ?refresh=1 or the cache is older than its TTL. Discovery is
// environment-only; ?exec=1 additionally runs one --version per runtime.
func (s *Server) handleRuntimes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		refresh := r.URL.Query().Get("refresh") == "1"
		exec := r.URL.Query().Get("exec") == "1"
		out, err := s.svc.Runtimes(r.Context(), refresh, exec)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var body struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Command string `json:"command"`
			Ext     string `json:"ext"`
			Version string `json:"version"`
			Exec    bool   `json:"exec"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
			return
		}
		snapshot, err := s.svc.AddRuntime(r.Context(), body.ID, body.Name, body.Command, body.Ext, body.Version, body.Exec)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
	}
}

// handleRuntimeRemove deletes a user-added language row.
func (s *Server) handleRuntimeRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
		return
	}
	snapshot, err := s.svc.RemoveRuntime(body.ID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}
