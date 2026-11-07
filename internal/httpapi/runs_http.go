package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

func (s *Server) registerRunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/runs/cancel", s.handleRunCancel)
	mux.HandleFunc("/api/runs/clear", s.handleRunClear)
	mux.HandleFunc("/api/runlog", s.handleRunLog)
}

// handleRuns lists recent runs from every toolbox process that shares the root,
// so the page can show what the agent is running right now.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	runs, err := s.svc.Runs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	running := 0
	for _, run := range runs {
		if run.Record.Running() {
			running++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "running": running})
}

// handleRunLog returns one run with the tail of its console output.
func (s *Server) handleRunLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	tail := 24_000
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			tail = parsed
		}
	}
	if tail < 1_000 {
		tail = 1_000
	}
	if tail > 400_000 {
		tail = 400_000
	}
	view, err := s.svc.RunLog(r.URL.Query().Get("id"), tail)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleRunCancel kills a running command, including one the agent started.
func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
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
	if err := s.svc.CancelRun(body.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": body.ID})
}

// handleRunClear drops settled records so the panel does not grow forever.
func (s *Server) handleRunClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method-not-allowed"})
		return
	}
	removed, err := s.svc.ClearRuns()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}
