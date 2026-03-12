package httphandlers

import (
	"fmt"
	"net/http"
)

func (s *Server) handleExportCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	incremental := r.URL.Query().Get("incremental") == "true"

	filename, err := s.cacheSvc.ExportCache(w, incremental)
	if err != nil {
		// Log error, but writer stream might have already been sent partially.
		_ = err
	}

	if filename == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
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
