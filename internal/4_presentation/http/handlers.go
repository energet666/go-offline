package httphandlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		d := time.Since(start)
		log.Printf("%s %s %d (%s)", r.Method, r.URL.Path, rec.status, d)
		if isProxyRequestPath(r.URL.Path) {
			s.appendProxyLog(fmt.Sprintf(
				"%s #%d %s %d %s %s ua=%q",
				time.Now().Format("15:04:05"),
				atomic.AddUint64(&s.proxyLogSeq, 1),
				r.Method,
				rec.status,
				d.Round(time.Millisecond),
				r.URL.Path,
				r.UserAgent(),
			))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func isProxyRequestPath(path string) bool {
	return path != "/" && !strings.HasPrefix(path, "/api/")
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/":
		f, err := uiTemplateFS.Open("web/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, _ := f.Stat()
		http.ServeContent(w, r, "index.html", stat.ModTime(), f.(io.ReadSeeker))
		return
	case strings.HasPrefix(r.URL.Path, "/assets/"):
		fsys, err := fs.Sub(uiTemplateFS, "web/assets")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.StripPrefix("/assets/", http.FileServer(http.FS(fsys))).ServeHTTP(w, r)
		return
	case strings.HasPrefix(r.URL.Path, "/api/"):
		http.NotFound(w, r)
		return
	default:
		s.ServeProxyFile(w, r)
	}
}

func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	rows, unexportedCount, err := s.cacheSvc.ListCachedModules(r.URL.Query().Get("q"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"modules":          rows,
		"unexported_count": unexportedCount,
	})
}

// startDownload starts a background download, returning an error if one is already running.
func (s *Server) startDownload(kind string, work func(logf func(string, ...any)) error) error {
	s.dlState.mu.Lock()
	if s.dlState.Status == "running" {
		s.dlState.mu.Unlock()
		return fmt.Errorf("download already in progress")
	}
	s.dlState.Status = "running"
	s.dlState.Error = ""
	s.dlState.Message = kind
	s.dlState.Logs = []string{}
	s.dlState.StartedAt = time.Now().Format(time.RFC3339)
	s.dlState.FinishedAt = ""
	s.dlState.mu.Unlock()

	go func() {
		err := work(s.dlState.logf)

		// Write directly to fields under lock — do NOT call logf() here,
		// because logf() also locks mu and Go mutexes are not reentrant.
		s.dlState.mu.Lock()
		s.dlState.FinishedAt = time.Now().Format(time.RFC3339)
		ts := time.Now().Format("15:04:05")
		if err != nil {
			s.dlState.Status = "error"
			s.dlState.Error = err.Error()
			s.dlState.Logs = append(s.dlState.Logs, ts+" error: "+err.Error())
		} else {
			s.dlState.Status = "done"
			s.dlState.Message = "Готово"
			s.dlState.Logs = append(s.dlState.Logs, ts+" done")
		}
		s.dlState.mu.Unlock()
	}()
	return nil
}

func (s *Server) handlePrefetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req prefetchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	req.Module = strings.TrimSpace(req.Module)
	req.Version = strings.TrimSpace(req.Version)
	if req.Module == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "module is required"})
		return
	}

	// Resolve package path -> module path before pinning or starting the download.
	resolveCtx, resolveCancel := context.WithTimeout(r.Context(), 30*time.Second)
	resolvedModule, resolveErr := s.resolveModulePath(resolveCtx, req.Module, req.Version)
	resolveCancel()
	if resolveErr != nil {
		log.Printf("warn: resolve module path %s: %v", req.Module, resolveErr)
	} else if resolvedModule != req.Module {
		log.Printf("info: resolved package path %s -> module %s", req.Module, resolvedModule)
		req.Module = resolvedModule
	}

	// Pin the module.
	if err := s.pinnedRepo.Pin(req.Module, req.Version); err != nil {
		log.Printf("warn: pin package: %v", err)
	}

	err := s.startDownload("module: "+req.Module, func(logf func(string, ...any)) error {
		logf("start module=%s version=%s recursive=%t", req.Module, req.Version, req.Recursive)
		if err := s.downloader.DownloadModule(context.Background(), req.Module, req.Version, req.Recursive, logf); err != nil {
			return err
		}
		// Resolve pinned version if "latest" was requested.
		if strings.TrimSpace(req.Version) == "" {
			if resolved, err := s.resolveVersionFromCache(req.Module); err == nil && resolved != "" {
				logf("resolved pinned version %s -> %s", req.Module, resolved)
				s.pinnedRepo.ResolvePinnedLatest(req.Module, resolved)
			}
		}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handlePrefetchGoMod(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req prefetchFromGoModRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if strings.TrimSpace(req.GoMod) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gomod is required"})
		return
	}

	requires, err := parseGoModRequires(req.GoMod)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid go.mod: %v", err)})
		return
	}

	// Pin all requires.
	for _, r := range requires {
		if err := s.pinnedRepo.Pin(r.Path, r.Version); err != nil {
			log.Printf("warn: pin package %s@%s: %v", r.Path, r.Version, err)
		}
	}

	dlErr := s.startDownload("go.mod", func(logf func(string, ...any)) error {
		logf("start requires=%d recursive=%t", len(requires), req.Recursive)
		return s.downloader.DownloadGoMod(context.Background(), req.GoMod, req.Recursive, logf)
	})
	if dlErr != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": dlErr.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleDownloadStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	snap := s.dlState.snapshot()
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleProxyRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	lines := s.snapshotProxyLogs(limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"lines": lines,
		"count": len(lines),
	})
}
