package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/haikuotianko/dsh-toolboxes/internal/install"
)

func (s *Server) registerScriptRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/scripts", s.handleScripts)
	mux.HandleFunc("/api/script", s.handleScript)
}

// handleScripts lists archived scripts with their source.
func (s *Server) handleScripts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	scripts, err := s.svc.ListScripts()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scripts": scripts})
}

type scriptBody struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
	Language    string `json:"language"`
	Content     string `json:"content"`
}

// handleScript creates/updates (POST) or deletes (DELETE) one script.
func (s *Server) handleScript(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body scriptBody
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
			return
		}
		view, err := s.svc.SaveScriptFromPage(install.ScriptSpec{
			ID: body.Name, Title: body.Title, Description: body.Description,
			Usage: body.Usage, Language: body.Language, Content: body.Content,
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, view)
	case http.MethodDelete:
		var body struct {
			Tool string `json:"tool"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid-json: " + err.Error()})
			return
		}
		if err := s.svc.DeleteScript(body.Tool); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": body.Tool})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
	}
}
