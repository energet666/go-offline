package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func (s *server) logRequests(next http.Handler) http.Handler {
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

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/":
		data := map[string]string{
			"ProxyURL": "http://" + r.Host,
		}
		_ = s.tmpl.Execute(w, data)
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
		s.serveProxyFile(w, r)
	}
}

func (s *server) handleModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	rows, err := s.listCachedModules(r.URL.Query().Get("q"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *server) handlePrefetch(w http.ResponseWriter, r *http.Request) {
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

	job := s.newJob("prefetch-module")
	job.logf("start module=%s version=%s recursive=%t", req.Module, valueOr(req.Version, "latest"), req.Recursive)

	// Record user-requested package before starting the job.
	if err := s.pinPackage(req.Module, req.Version); err != nil {
		log.Printf("warn: pin package: %v", err)
	}

	go func() {
		err := s.prefetchModuleWithGo(context.Background(), req.Module, req.Version, req.Recursive, job.logf)
		if err != nil {
			job.fail(err)
			return
		}
		job.complete(prefetchReport{}, "done via go tool")
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": job.ID, "state": job.State})
}

func (s *server) handlePrefetchGoMod(w http.ResponseWriter, r *http.Request) {
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

	job := s.newJob("prefetch-gomod")
	job.logf("start requires=%d recursive=%t", len(requires), req.Recursive)

	// Pin all packages from go.mod as user-requested.
	for _, r := range requires {
		if err := s.pinPackage(r.Path, r.Version); err != nil {
			log.Printf("warn: pin package %s@%s: %v", r.Path, r.Version, err)
		}
	}

	go func() {
		err := s.prefetchGoModWithGo(context.Background(), req.GoMod, req.Recursive, job.logf)
		if err != nil {
			job.fail(err)
			return
		}
		job.complete(prefetchReport{}, "done via go tool")
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": job.ID, "state": job.State})
}

func (s *server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
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
	job, ok := s.getJob(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job.snapshot())
}

func (s *server) handleProxyRequests(w http.ResponseWriter, r *http.Request) {
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
