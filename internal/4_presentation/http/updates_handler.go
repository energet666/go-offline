package httphandlers

import (
	"context"
	"net/http"
	"time"

	"go-offline/internal/1_domain/cache"
)

// checkUpdatesTimeout ограничивает общее время похода в апстрим за версиями.
const checkUpdatesTimeout = 90 * time.Second

// handlePinnedUpdates сообщает, для каких закреплённых модулей в апстриме
// появились версии новее закреплённых. Требует доступа в интернет.
func (s *Server) handlePinnedUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.updateChecker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "update checker is not configured"})
		return
	}

	force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"

	entries := s.pinnedRepo.List()
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, cache.UpdatesReport{
			CheckedAt: time.Now().Format(time.RFC3339),
			Updates:   []cache.ModuleUpdate{},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), checkUpdatesTimeout)
	defer cancel()

	report, err := s.updateChecker.CheckUpdates(ctx, entries, force)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}
