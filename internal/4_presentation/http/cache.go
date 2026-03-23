package httphandlers

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

func (s *Server) handleExportPrepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	incremental := r.URL.Query().Get("incremental") == "true"
	filename := exportFilename(incremental)

	exportDir := filepath.Join(s.workDir, "exports")
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create export dir: " + err.Error()})
		return
	}

	// Clean up old archives (> 1 hour)
	if entries, err := os.ReadDir(exportDir); err == nil {
		now := time.Now()
		for _, entry := range entries {
			if info, err := entry.Info(); err == nil && !info.IsDir() {
				if now.Sub(info.ModTime()) > time.Hour {
					_ = os.Remove(filepath.Join(exportDir, entry.Name()))
				}
			}
		}
	}

	tempPath := filepath.Join(exportDir, filename)
	f, err := os.Create(tempPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create export archive: " + err.Error()})
		return
	}

	if err := s.cacheRepo.Export(f, incremental); err != nil {
		f.Close()
		os.Remove(tempPath)
		if errors.Is(err, cache.ErrNoNewFiles) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "export failed: " + err.Error()})
		return
	}
	f.Close()

	writeJSON(w, http.StatusOK, map[string]string{
		"download_url": fmt.Sprintf("/api/export-cache/download?file=%s", filename),
		"filename":     filename,
	})
}

func (s *Server) handleExportDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	filename := r.URL.Query().Get("file")
	if filename == "" || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid filename"})
		return
	}

	filePath := filepath.Join(s.workDir, "exports", filename)
	f, err := os.Open(filePath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "archive not found: " + err.Error()})
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	http.ServeContent(w, r, filename, time.Now(), f)
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
