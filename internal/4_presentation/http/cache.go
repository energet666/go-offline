package httphandlers

import (
	"errors"
	"fmt"
	"net/http"

	application "go-offline/internal/2_application"
	"go-offline/internal/1_domain/cache"
)

func (s *Server) handleExportCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	incremental := r.URL.Query().Get("incremental") == "true"

	filename := application.ExportFilename(incremental)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	err := s.cacheSvc.ExportCache(w, incremental)
	if err != nil {
		if errors.Is(err, cache.ErrNoNewFiles) {
			// Reset headers and send 204 No Content.
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Disposition")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Data may have already been partially written; log the error.
		_ = err
	}
}

func (s *Server) handleImportCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Limit upload size to 4 GB.
	r.Body = http.MaxBytesReader(w, r.Body, 4<<30)

	file, _, err := r.FormFile("archive")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid archive file: " + err.Error()})
		return
	}
	defer file.Close()

	extractedFiles, err := s.cacheSvc.ImportCache(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"extracted_files": extractedFiles,
		"message":         fmt.Sprintf("Импортировано %d файлов", extractedFiles),
	})
}
