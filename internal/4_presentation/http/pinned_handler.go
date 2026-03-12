package httphandlers

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handlePinned(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		entries := s.pinnedRepo.List()
		writeJSON(w, http.StatusOK, entries)
	case http.MethodDelete:
		var req struct {
			Module  string `json:"module"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		if req.Module == "" || req.Version == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "module and version required"})
			return
		}
		if err := s.pinnedRepo.Unpin(req.Module, req.Version); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) appendProxyLog(line string) {
	s.proxyLogsMu.Lock()
	s.proxyLogs = append(s.proxyLogs, line)
	if len(s.proxyLogs) > 500 {
		s.proxyLogs = s.proxyLogs[len(s.proxyLogs)-500:]
	}
	s.proxyLogsMu.Unlock()
}

func (s *Server) snapshotProxyLogs(limit int) []string {
	s.proxyLogsMu.Lock()
	defer s.proxyLogsMu.Unlock()
	if limit <= 0 || limit > len(s.proxyLogs) {
		limit = len(s.proxyLogs)
	}
	start := len(s.proxyLogs) - limit
	return append([]string(nil), s.proxyLogs[start:]...)
}
