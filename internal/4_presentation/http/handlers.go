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

	rows, err := s.cacheSvc.ListCachedModules(r.URL.Query().Get("q"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rows)
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

	// Resolve package path -> module path before pinning or starting the job.
	// Use a short timeout so we don't block the HTTP response too long.
	resolveCtx, resolveCancel := context.WithTimeout(r.Context(), 30*time.Second)
	resolvedModule, resolveErr := s.resolveModulePath(resolveCtx, req.Module, req.Version)
	resolveCancel()
	if resolveErr != nil {
		log.Printf("warn: resolve module path %s: %v", req.Module, resolveErr)
	} else if resolvedModule != req.Module {
		log.Printf("info: resolved package path %s -> module %s", req.Module, resolvedModule)
		req.Module = resolvedModule
	}

	id, err := s.prefetchSvc.PrefetchModule(context.Background(), req.Module, req.Version, req.Recursive)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
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

	var reqRequires []struct {
		Path    string
		Version string
	}
	for _, r := range requires {
		reqRequires = append(reqRequires, struct {
			Path    string
			Version string
		}{Path: r.Path, Version: r.Version})
	}
	id, err := s.prefetchSvc.PrefetchGoMod(context.Background(), req.GoMod, req.Recursive, reqRequires)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
}

func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "job id is required"})
		return
	}
	job, err := s.prefetchSvc.GetJob(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job.Snapshot())
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
