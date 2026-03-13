package httphandlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"go-offline/internal/1_domain/cache"
)

// exportFilename returns a descriptive archive filename for the given export kind.
func exportFilename(incremental bool) string {
	ts := time.Now().Format("2006-01-02_15-04-05")
	kind := "full"
	if incremental {
		kind = "incremental"
	}
	return fmt.Sprintf("go-offline-%s-%s.tar.gz", kind, ts)
}

func (s *Server) handleExportCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	incremental := r.URL.Query().Get("incremental") == "true"

	filename := exportFilename(incremental)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	err := s.cacheRepo.Export(w, incremental)
	if err != nil {
		if errors.Is(err, cache.ErrNoNewFiles) {
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Disposition")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = err
	}
}

func (s *Server) handleImportCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<30)

	file, _, err := r.FormFile("archive")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid archive file: " + err.Error()})
		return
	}
	defer file.Close()

	extractedFiles, err := s.cacheRepo.Import(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Архив может содержать обновлённый user-packages.json —
	// перечитываем закреплённые пакеты из файла.
	if reloadErr := s.pinnedRepo.Reload(); reloadErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("imported %d files but failed to reload pinned packages: %v", extractedFiles, reloadErr),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"extracted_files": extractedFiles,
		"message":         fmt.Sprintf("Импортировано %d файлов", extractedFiles),
	})
}
