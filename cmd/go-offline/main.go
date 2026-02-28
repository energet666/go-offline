package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type server struct {
	cacheDir     string
	workDir      string
	upstream     string
	httpClient   *http.Client
	fetchRetries int
	retryBackoff time.Duration
	goBin        string
	maxJobBytes  int64
	maxModules   int64
	tmpl         *template.Template
	jobsMu       sync.RWMutex
	jobs         map[string]*jobState
	jobSeq       uint64
	proxyLogsMu  sync.Mutex
	proxyLogs    []string
	proxyLogSeq  uint64
	pinnedMu     sync.RWMutex
	pinnedPkgs   map[string]pinnedEntry // key: "module@version"
}

// pinnedEntry represents a package explicitly requested by the user.
type pinnedEntry struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	PinnedAt string `json:"pinned_at"`
}

type cachedModule struct {
	Module   string `json:"module"`
	Version  string `json:"version"`
	Time     string `json:"time,omitempty"`
	Pinned   bool   `json:"pinned,omitempty"`
	Exported bool   `json:"exported,omitempty"`
}

type prefetchRequest struct {
	Module    string `json:"module"`
	Version   string `json:"version"`
	Recursive bool   `json:"recursive"`
}

type prefetchFromGoModRequest struct {
	GoMod     string `json:"gomod"`
	Recursive bool   `json:"recursive"`
}

type modReq struct {
	Path    string
	Version string
}

type listedModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Main    bool          `json:"Main"`
	Replace *listedModule `json:"Replace,omitempty"`
}

type prefetchReport struct {
	Downloaded []string `json:"downloaded"`
	Skipped    []string `json:"skipped"`
}

type fetchBudget struct {
	maxBytes   int64
	maxModules int64
	bytes      int64
	modules    int64
}

type jobState struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	State      string   `json:"state"`
	Message    string   `json:"message,omitempty"`
	Error      string   `json:"error,omitempty"`
	Downloaded []string `json:"downloaded"`
	Skipped    []string `json:"skipped"`
	Logs       []string `json:"logs"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at,omitempty"`

	mu sync.Mutex
}

func main() {
	var (
		listen       = flag.String("listen", ":8080", "HTTP listen address")
		cacheDir     = flag.String("cache", "./cache", "cache directory (persistent, for export/import)")
		workDir      = flag.String("workdir", "./workdir", "working directory (ephemeral: gocache, proxy, tmp)")
		upstream     = flag.String("upstream", "https://proxy.golang.org", "upstream GOPROXY")
		httpTimeout  = flag.Duration("http-timeout", 5*time.Minute, "HTTP timeout for upstream requests")
		fetchRetries = flag.Int("fetch-retries", 3, "retries for timeout/429/5xx upstream errors")
		maxJobBytes  = flag.Int64("max-job-bytes", 2*1024*1024*1024, "max downloaded bytes per prefetch job (0 disables)")
		maxModules   = flag.Int64("max-job-modules", 4000, "max processed modules per prefetch job (0 disables)")
		goBin        = flag.String("go-bin", "go", "path to go binary")
	)
	flag.Parse()

	absCacheDir, err := filepath.Abs(*cacheDir)
	if err != nil {
		log.Fatalf("resolve cache dir: %v", err)
	}
	*cacheDir = absCacheDir

	absWorkDir, err := filepath.Abs(*workDir)
	if err != nil {
		log.Fatalf("resolve workdir: %v", err)
	}
	*workDir = absWorkDir

	// Persistent cache directories.
	if err := os.MkdirAll(filepath.Join(*cacheDir, "gomodcache", "cache", "download"), 0o755); err != nil {
		log.Fatalf("create gomodcache dir: %v", err)
	}

	// Ephemeral working directories.
	if err := os.MkdirAll(filepath.Join(*workDir, "proxy"), 0o755); err != nil {
		log.Fatalf("create proxy dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*workDir, "gocache"), 0o755); err != nil {
		log.Fatalf("create gocache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*workDir, "tmp"), 0o755); err != nil {
		log.Fatalf("create tmp dir: %v", err)
	}

	s := &server{
		cacheDir: *cacheDir,
		workDir:  *workDir,
		upstream: strings.TrimRight(*upstream, "/"),
		httpClient: &http.Client{
			Timeout: *httpTimeout,
		},
		fetchRetries: *fetchRetries,
		retryBackoff: 2 * time.Second,
		goBin:        *goBin,
		maxJobBytes:  *maxJobBytes,
		maxModules:   *maxModules,
		tmpl:         uiTmpl,
		jobs:         make(map[string]*jobState),
	}

	// Load pinned packages from disk.
	if err := s.loadPinnedPackages(); err != nil {
		log.Printf("warn: load pinned packages: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/modules", s.handleModules)
	mux.HandleFunc("/api/prefetch", s.handlePrefetch)
	mux.HandleFunc("/api/prefetch-gomod", s.handlePrefetchGoMod)
	mux.HandleFunc("/api/jobs/", s.handleJobStatus)
	mux.HandleFunc("/api/proxy-requests", s.handleProxyRequests)
	mux.HandleFunc("/api/pinned", s.handlePinned)
	mux.HandleFunc("/api/export-cache", s.handleExportCache)
	mux.HandleFunc("/api/import-cache", s.handleImportCache)

	log.Printf("go-offline started on %s", *listen)
	log.Printf("cache directory: %s", *cacheDir)
	log.Printf("work directory: %s", *workDir)
	log.Printf("upstream timeout: %s retries: %d", (*httpTimeout).String(), *fetchRetries)
	log.Printf("job limits: max-bytes=%d max-modules=%d", *maxJobBytes, *maxModules)
	log.Printf("go binary: %s", *goBin)
	log.Printf("set GOPROXY=http://127.0.0.1%s", *listen)
	if err := http.ListenAndServe(*listen, s.logRequests(mux)); err != nil {
		log.Fatal(err)
	}
}

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

func (s *server) prefetchModule(ctx context.Context, modPath, version string, recursive bool, seen map[string]struct{}, budget *fetchBudget, logf func(string, ...any)) (prefetchReport, error) {
	type task struct {
		modPath string
		version string
	}
	rootKey := modPath + "@" + valueOr(version, "latest")
	if seen == nil {
		seen = map[string]struct{}{}
	}
	var (
		report prefetchReport
		queue  = []task{{modPath: modPath, version: version}}
		queued = map[string]struct{}{rootKey: {}}
	)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		logf("resolve %s@%s", cur.modPath, valueOr(cur.version, "latest"))

		resolvedVersion, err := s.resolveVersion(ctx, cur.modPath, cur.version)
		if err != nil {
			return report, err
		}
		key := cur.modPath + "@" + resolvedVersion
		if _, ok := seen[key]; ok {
			logf("skip already processed %s", key)
			continue
		}
		seen[key] = struct{}{}
		if err := budget.noteModule(); err != nil {
			return report, err
		}

		downloaded, modContent, downloadedBytes, err := s.downloadModule(ctx, cur.modPath, resolvedVersion, logf)
		if err != nil {
			return report, err
		}
		if downloaded {
			report.Downloaded = append(report.Downloaded, key)
			if err := budget.noteBytes(downloadedBytes); err != nil {
				return report, err
			}
			logf("downloaded %s size=%s total=%s", key, humanBytes(downloadedBytes), humanBytes(budget.bytes))
		} else {
			report.Skipped = append(report.Skipped, key)
			logf("cached %s", key)
		}

		if !recursive {
			continue
		}
		reqs, err := parseGoModRequires(string(modContent))
		if err != nil {
			continue
		}
		for _, reqMod := range reqs {
			nextKey := reqMod.Path + "@" + reqMod.Version
			if _, ok := seen[nextKey]; ok {
				continue
			}
			if _, ok := queued[nextKey]; ok {
				continue
			}
			queued[nextKey] = struct{}{}
			queue = append(queue, task{modPath: reqMod.Path, version: reqMod.Version})
			logf("queue dep %s", nextKey)
		}
	}

	sort.Strings(report.Downloaded)
	sort.Strings(report.Skipped)
	return report, nil
}

func (s *server) resolveVersion(ctx context.Context, modPath, requestedVersion string) (string, error) {
	if requestedVersion != "" && requestedVersion != "latest" {
		return requestedVersion, nil
	}

	escapedPath, err := escapeModulePath(modPath)
	if err != nil {
		return "", err
	}
	u := s.upstream + "/" + escapedPath + "/@latest"
	body, err := s.fetchRemote(ctx, u, nil)
	if err != nil {
		return "", err
	}
	var latest struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(body, &latest); err != nil {
		return "", err
	}
	if latest.Version == "" {
		return "", errors.New("empty latest version")
	}
	return latest.Version, nil
}

func (s *server) downloadModule(ctx context.Context, modPath, version string, logf func(string, ...any)) (bool, []byte, int64, error) {
	escapedPath, err := escapeModulePath(modPath)
	if err != nil {
		return false, nil, 0, err
	}
	escapedVersion, err := escapeModuleVersion(version)
	if err != nil {
		return false, nil, 0, err
	}

	dir := filepath.Join(s.workDir, "proxy", filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil, 0, err
	}
	modFile := filepath.Join(dir, escapedVersion+".mod")
	if _, err := os.Stat(modFile); err == nil {
		content, readErr := os.ReadFile(modFile)
		return false, content, 0, readErr
	}

	infoURL := s.upstream + "/" + escapedPath + "/@v/" + escapedVersion + ".info"
	modURL := s.upstream + "/" + escapedPath + "/@v/" + escapedVersion + ".mod"
	zipURL := s.upstream + "/" + escapedPath + "/@v/" + escapedVersion + ".zip"

	infoBody, err := s.fetchRemote(ctx, infoURL, logf)
	if err != nil {
		return false, nil, 0, err
	}
	logf("fetched info %s@%s", modPath, version)
	modBody, err := s.fetchRemote(ctx, modURL, logf)
	if err != nil {
		return false, nil, 0, err
	}
	logf("fetched mod %s@%s", modPath, version)
	zipBody, err := s.fetchRemote(ctx, zipURL, logf)
	if err != nil {
		return false, nil, 0, err
	}
	logf("fetched zip %s@%s", modPath, version)

	if err := os.WriteFile(filepath.Join(dir, escapedVersion+".info"), infoBody, 0o644); err != nil {
		return false, nil, 0, err
	}
	if err := os.WriteFile(modFile, modBody, 0o644); err != nil {
		return false, nil, 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, escapedVersion+".zip"), zipBody, 0o644); err != nil {
		return false, nil, 0, err
	}
	if err := s.updateVersionList(dir, version); err != nil {
		return false, nil, 0, err
	}
	total := int64(len(infoBody) + len(modBody) + len(zipBody))
	return true, modBody, total, nil
}

func (s *server) fetchRemote(ctx context.Context, rawURL string, logf func(string, ...any)) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= s.fetchRetries; attempt++ {
		if attempt > 0 && logf != nil {
			logf("retry %d/%d for %s", attempt, s.fetchRetries, rawURL)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < s.fetchRetries && isRetryableError(err) {
				time.Sleep(s.retryBackoff * time.Duration(attempt+1))
				continue
			}
			return nil, err
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < s.fetchRetries && isRetryableError(readErr) {
				time.Sleep(s.retryBackoff * time.Duration(attempt+1))
				continue
			}
			return nil, readErr
		}
		if resp.StatusCode != http.StatusOK {
			msg := fmt.Errorf("upstream %s: %s (%s)", rawURL, resp.Status, strings.TrimSpace(string(limitBytes(body, 2048))))
			lastErr = msg
			if attempt < s.fetchRetries && isRetryableStatus(resp.StatusCode) {
				time.Sleep(s.retryBackoff * time.Duration(attempt+1))
				continue
			}
			return nil, msg
		}
		return body, nil
	}
	if lastErr == nil {
		lastErr = errors.New("upstream request failed")
	}
	return nil, lastErr
}

func (s *server) prefetchModuleWithGo(ctx context.Context, modPath, version string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(s.workDir, "tmp"), "prefetch-module-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	// The user may have entered a package path (e.g. golang.org/x/net/websocket)
	// instead of a module path (e.g. golang.org/x/net). Resolve to the actual
	// module path by probing the upstream proxy.
	resolvedModule, err := s.resolveModulePath(ctx, modPath, version)
	if err != nil {
		return fmt.Errorf("resolve module path for %q: %w", modPath, err)
	}
	if resolvedModule != modPath {
		logf("resolved package path %s -> module %s", modPath, resolvedModule)
		modPath = resolvedModule
	}

	target := modPath + "@" + valueOr(version, "latest")
	logf("go prefetch target %s", target)

	if err := s.runGoCmd(ctx, workdir, logf, "mod", "init", "prefetch.local/job"); err != nil {
		return err
	}
	if err := s.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
		return err
	}

	// If the user requested "latest" (no explicit version), resolve the real version
	// from the downloaded .info file and update the pinned entry accordingly.
	if strings.TrimSpace(version) == "" {
		if resolved, err := s.resolveVersionFromCache(modPath); err == nil && resolved != "" {
			logf("resolved pinned version %s -> %s", modPath, resolved)
			s.resolvePinnedLatest(modPath, resolved)
		}
	}

	if recursive {
		// go get adds module to go.mod; then mod download pulls required graph.
		// Avoid `download all` because it may fail on broken transitive/test-only revisions.
		if err := s.runGoCmd(ctx, workdir, logf, "get", target); err != nil {
			return err
		}
		if err := s.runGoCmd(ctx, workdir, logf, "mod", "download"); err != nil {
			return err
		}
		s.prefetchModuleGraphBestEffort(ctx, workdir, logf)
	}
	return nil
}

// resolveModulePath resolves a package path to the root module path by probing
// the upstream GOPROXY. It tries successively shorter path prefixes (from
// longest to shortest) until it finds one that the proxy recognises as a valid
// module. If the path itself is already a valid module path it is returned
// unchanged.
func (s *server) resolveModulePath(ctx context.Context, pkgPath, version string) (string, error) {
	parts := strings.Split(pkgPath, "/")
	// Try from longest to shortest prefix.
	// We start at the full path and walk up to the root.
	for i := len(parts); i >= 1; i-- {
		candidate := strings.Join(parts[:i], "/")
		escapedPath, err := escapeModulePath(candidate)
		if err != nil {
			continue
		}
		var probeURL string
		if version != "" && version != "latest" {
			// Probe a specific version via /@v/<version>.info
			escapedVer, err := escapeModuleVersion(version)
			if err != nil {
				continue
			}
			probeURL = s.upstream + "/" + escapedPath + "/@v/" + escapedVer + ".info"
		} else {
			// Probe the latest endpoint
			probeURL = s.upstream + "/" + escapedPath + "/@latest"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err != nil {
			continue
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return candidate, nil
		}
	}
	// Fallback: return the original path; go mod download will produce a clear error.
	return pkgPath, nil
}

// resolveVersionFromCache finds the latest cached version of modPath by scanning
// the proxy cache directory for .info files.
func (s *server) resolveVersionFromCache(modPath string) (string, error) {
	escapedPath, err := escapeModulePath(modPath)
	if err != nil {
		return "", err
	}
	versionDir := filepath.Join(s.proxyBaseDir(), filepath.FromSlash(escapedPath), "@v")
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return "", err
	}
	var best string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".info") {
			continue
		}
		escapedVer := strings.TrimSuffix(ent.Name(), ".info")
		ver, err := unescapeModuleVersion(escapedVer)
		if err != nil {
			ver = escapedVer
		}
		if best == "" || compareModuleVersions(ver, best) > 0 {
			best = ver
		}
	}
	if best == "" {
		return "", errors.New("no cached versions found")
	}
	return best, nil
}

func (s *server) prefetchGoModWithGo(ctx context.Context, goMod string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(s.workDir, "tmp"), "prefetch-gomod-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	if err := os.WriteFile(filepath.Join(workdir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return err
	}
	if recursive {
		// Use `go mod download` instead of `download all` for better resilience when
		// dependency graphs contain unreachable historical revisions.
		if err := s.runGoCmd(ctx, workdir, logf, "mod", "download"); err != nil {
			return err
		}
		s.prefetchModuleGraphBestEffort(ctx, workdir, logf)
		return nil
	}

	reqs, err := parseGoModRequires(goMod)
	if err != nil {
		return err
	}
	for _, req := range reqs {
		target := req.Path + "@" + req.Version
		if err := s.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) runGoCmd(ctx context.Context, workdir string, logf func(string, ...any), args ...string) error {
	_, err := s.runGoCmdOutput(ctx, workdir, logf, args...)
	return err
}

func (s *server) runGoCmdOutput(ctx context.Context, workdir string, logf func(string, ...any), args ...string) ([]byte, error) {
	logf("$ %s %s", s.goBin, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, s.goBin, args...)
	cmd.Dir = workdir
	cmd.Env = s.goEnv()
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) != "" {
				logf("go: %s", line)
			}
		}
	}
	if err != nil {
		return out, fmt.Errorf("command failed: %s %s: %w", s.goBin, strings.Join(args, " "), err)
	}
	return out, nil
}

func (s *server) prefetchModuleGraphBestEffort(ctx context.Context, workdir string, logf func(string, ...any)) {
	out, err := s.runGoCmdOutput(ctx, workdir, logf, "list", "-m", "-json", "all")
	if err != nil {
		logf("warn: unable to enumerate module graph: %v", err)
		return
	}

	dec := json.NewDecoder(strings.NewReader(string(out)))
	seen := make(map[string]struct{})
	attempted := 0
	failed := 0
	for {
		var m listedModule
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			logf("warn: unable to parse module graph: %v", err)
			return
		}

		resolved := &m
		if m.Replace != nil {
			resolved = m.Replace
		}
		if resolved.Main || resolved.Version == "" {
			continue
		}
		target := resolved.Path + "@" + resolved.Version
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		attempted++
		if err := s.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
			failed++
			logf("warn: skip %s: %v", target, err)
		}
	}
	if attempted > 0 {
		logf("graph prefetch completed: attempted=%d failed=%d", attempted, failed)
	}
}

func (s *server) goEnv() []string {
	env := os.Environ()
	env = append(env, "GOMODCACHE="+filepath.Join(s.cacheDir, "gomodcache"))
	env = append(env, "GOCACHE="+filepath.Join(s.workDir, "gocache"))
	env = append(env, "GONOSUMDB=*")
	return env
}

func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= http.StatusInternalServerError
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "temporary")
}

func limitBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

func (s *server) proxyBaseDir() string {
	newBase := filepath.Join(s.cacheDir, "gomodcache", "cache", "download")
	if st, err := os.Stat(newBase); err == nil && st.IsDir() {
		return newBase
	}
	return filepath.Join(s.workDir, "proxy")
}

func (s *server) updateVersionList(versionDir, version string) error {
	listFile := filepath.Join(versionDir, "list")
	set := map[string]struct{}{version: {}}
	if data, err := os.ReadFile(listFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				set[line] = struct{}{}
			}
		}
	}
	var versions []string
	for v := range set {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	out := strings.Join(versions, "\n")
	if out != "" {
		out += "\n"
	}
	return os.WriteFile(listFile, []byte(out), 0o644)
}

func (s *server) serveProxyFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rel := strings.TrimPrefix(r.URL.Path, "/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	rel, err := url.PathUnescape(rel)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if strings.HasSuffix(rel, "/@latest") {
		if s.serveLatestFromCache(w, r, strings.TrimSuffix(rel, "/@latest")) {
			return
		}
		http.NotFound(w, r)
		return
	}
	target := filepath.Join(s.proxyBaseDir(), filepath.FromSlash(rel))
	if st, err := os.Stat(target); err == nil && !st.IsDir() {
		http.ServeFile(w, r, target)
		return
	}
	legacyTarget := filepath.Join(s.workDir, "proxy", filepath.FromSlash(rel))
	if st, err := os.Stat(legacyTarget); err == nil && !st.IsDir() {
		http.ServeFile(w, r, legacyTarget)
		return
	}

	http.NotFound(w, r)
}

func (s *server) serveLatestFromCache(w http.ResponseWriter, r *http.Request, escapedModulePath string) bool {
	type latestCandidate struct {
		version string
		body    []byte
	}

	baseDirs := []string{s.proxyBaseDir(), filepath.Join(s.workDir, "proxy")}
	var best *latestCandidate

	for _, base := range baseDirs {
		versionDir := filepath.Join(base, filepath.FromSlash(escapedModulePath), "@v")
		entries, err := os.ReadDir(versionDir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".info") {
				continue
			}

			escapedVersion := strings.TrimSuffix(ent.Name(), ".info")
			version, err := unescapeModuleVersion(escapedVersion)
			if err != nil {
				version = escapedVersion
			}

			infoPath := filepath.Join(versionDir, ent.Name())
			body, err := os.ReadFile(infoPath)
			if err != nil {
				continue
			}

			if best == nil || compareModuleVersions(version, best.version) > 0 {
				best = &latestCandidate{
					version: version,
					body:    body,
				}
			}
		}
	}

	if best == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	http.ServeContent(w, r, "@latest", time.Time{}, strings.NewReader(string(best.body)))
	return true
}

func compareModuleVersions(a, b string) int {
	pa, oka := parseModuleVersion(a)
	pb, okb := parseModuleVersion(b)
	if !oka || !okb {
		return strings.Compare(a, b)
	}

	for i := 0; i < 3; i++ {
		if pa.core[i] != pb.core[i] {
			if pa.core[i] > pb.core[i] {
				return 1
			}
			return -1
		}
	}
	if pa.pre == "" && pb.pre == "" {
		return 0
	}
	if pa.pre == "" {
		return 1
	}
	if pb.pre == "" {
		return -1
	}
	return comparePreRelease(pa.pre, pb.pre)
}

type parsedModuleVersion struct {
	core [3]int64
	pre  string
}

func parseModuleVersion(v string) (parsedModuleVersion, bool) {
	if !strings.HasPrefix(v, "v") {
		return parsedModuleVersion{}, false
	}
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")

	main := v
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		main = v[:i]
		pre = v[i+1:]
	}

	parts := strings.Split(main, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return parsedModuleVersion{}, false
	}

	var core [3]int64
	for i := 0; i < len(parts); i++ {
		if parts[i] == "" {
			return parsedModuleVersion{}, false
		}
		n, err := strconv.ParseInt(parts[i], 10, 64)
		if err != nil {
			return parsedModuleVersion{}, false
		}
		core[i] = n
	}
	return parsedModuleVersion{core: core, pre: pre}, true
}

func comparePreRelease(a, b string) int {
	ai := strings.Split(a, ".")
	bi := strings.Split(b, ".")
	n := len(ai)
	if len(bi) < n {
		n = len(bi)
	}
	for i := 0; i < n; i++ {
		if ai[i] == bi[i] {
			continue
		}
		an, aNum := parseNumericIdentifier(ai[i])
		bn, bNum := parseNumericIdentifier(bi[i])
		if aNum && bNum {
			if an > bn {
				return 1
			}
			return -1
		}
		if aNum {
			return -1
		}
		if bNum {
			return 1
		}
		if ai[i] > bi[i] {
			return 1
		}
		return -1
	}
	if len(ai) == len(bi) {
		return 0
	}
	if len(ai) > len(bi) {
		return 1
	}
	return -1
}

func parseNumericIdentifier(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (s *server) listCachedModules(query string) ([]cachedModule, error) {
	base := s.proxyBaseDir()
	out := make([]cachedModule, 0, 128)
	query = strings.ToLower(strings.TrimSpace(query))

	exportStatePath := filepath.Join(s.cacheDir, ".export-state.json")
	exportedPaths := make(map[string]bool)
	if stateBytes, err := os.ReadFile(exportStatePath); err == nil {
		var st struct {
			Files map[string]bool `json:"files"`
		}
		if err := json.Unmarshal(stateBytes, &st); err == nil && st.Files != nil {
			exportedPaths = st.Files
		}
	}

	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".info") {
			return nil
		}

		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 3 || parts[len(parts)-2] != "@v" {
			return nil
		}

		escapedMod := strings.Join(parts[:len(parts)-2], "/")
		modPath, err := unescapeModulePath(escapedMod)
		if err != nil {
			modPath = escapedMod
		}

		escapedVer := strings.TrimSuffix(parts[len(parts)-1], ".info")
		ver, err := unescapeModuleVersion(escapedVer)
		if err != nil {
			ver = escapedVer
		}

		var info struct {
			Time time.Time `json:"Time"`
		}
		if data, readErr := os.ReadFile(path); readErr == nil {
			_ = json.Unmarshal(data, &info)
		}

		relFromCache := filepath.ToSlash(filepath.Join("gomodcache", "cache", "download", rel))

		row := cachedModule{
			Module:   modPath,
			Version:  ver,
			Pinned:   s.isPinned(modPath, ver),
			Exported: exportedPaths[relFromCache],
		}
		if !info.Time.IsZero() {
			row.Time = info.Time.Format(time.RFC3339)
		}
		if query != "" {
			moduleLC := strings.ToLower(row.Module)
			versionLC := strings.ToLower(row.Version)
			if !strings.Contains(moduleLC, query) && !strings.Contains(versionLC, query) {
				return nil
			}
		}
		out = append(out, row)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		// Pinned packages first, then alphabetical.
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if out[i].Module == out[j].Module {
			return out[i].Version < out[j].Version
		}
		return out[i].Module < out[j].Module
	})
	return out, nil
}

// pinnedFilePath returns the path to the pinned packages JSON file.
func (s *server) pinnedFilePath() string {
	return filepath.Join(s.cacheDir, "user-packages.json")
}

// loadPinnedPackages reads pinned packages from disk into memory.
func (s *server) loadPinnedPackages() error {
	data, err := os.ReadFile(s.pinnedFilePath())
	if os.IsNotExist(err) {
		s.pinnedMu.Lock()
		s.pinnedPkgs = make(map[string]pinnedEntry)
		s.pinnedMu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	var entries []pinnedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	m := make(map[string]pinnedEntry, len(entries))
	for _, e := range entries {
		m[e.Module+"@"+e.Version] = e
	}
	s.pinnedMu.Lock()
	s.pinnedPkgs = m
	s.pinnedMu.Unlock()
	return nil
}

// savePinnedPackagesLocked writes pinned packages to disk. Must be called with pinnedMu write-locked.
func (s *server) savePinnedPackagesLocked() error {
	entries := make([]pinnedEntry, 0, len(s.pinnedPkgs))
	for _, e := range s.pinnedPkgs {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		ki := entries[i].Module + "@" + entries[i].Version
		kj := entries[j].Module + "@" + entries[j].Version
		return ki < kj
	})
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.pinnedFilePath(), data, 0o644)
}

// pinPackage records a user-requested package. If the version is empty or
// "latest", it is stored as-is and updated on first resolution.
func (s *server) pinPackage(module, version string) error {
	key := module + "@" + valueOr(version, "latest")
	s.pinnedMu.Lock()
	defer s.pinnedMu.Unlock()
	if s.pinnedPkgs == nil {
		s.pinnedPkgs = make(map[string]pinnedEntry)
	}
	if _, ok := s.pinnedPkgs[key]; ok {
		return nil // already recorded
	}
	s.pinnedPkgs[key] = pinnedEntry{
		Module:   module,
		Version:  valueOr(version, "latest"),
		PinnedAt: time.Now().Format(time.RFC3339),
	}
	return s.savePinnedPackagesLocked()
}

// unpinPackage removes a package from the pinned list.
func (s *server) unpinPackage(module, version string) error {
	key := module + "@" + version
	s.pinnedMu.Lock()
	defer s.pinnedMu.Unlock()
	if _, ok := s.pinnedPkgs[key]; !ok {
		return nil
	}
	delete(s.pinnedPkgs, key)
	return s.savePinnedPackagesLocked()
}

// isPinned reports whether a module@version is in the pinned set.
// It also matches entries recorded as "latest" (before the real version was resolved).
func (s *server) isPinned(module, version string) bool {
	s.pinnedMu.RLock()
	defer s.pinnedMu.RUnlock()
	if _, ok := s.pinnedPkgs[module+"@"+version]; ok {
		return true
	}
	// Fallback: the user may have requested this module without a version,
	// so it was recorded as "latest" before the real version was resolved.
	_, ok := s.pinnedPkgs[module+"@latest"]
	return ok
}

// resolvePinnedLatest updates the "latest" pinned entry for module to the
// resolved concrete version. Safe to call from goroutines.
func (s *server) resolvePinnedLatest(module, resolvedVersion string) {
	latestKey := module + "@latest"
	s.pinnedMu.Lock()
	defer s.pinnedMu.Unlock()
	entry, ok := s.pinnedPkgs[latestKey]
	if !ok {
		return
	}
	// Replace placeholder "latest" with the real version.
	delete(s.pinnedPkgs, latestKey)
	newKey := module + "@" + resolvedVersion
	if _, exists := s.pinnedPkgs[newKey]; !exists {
		entry.Version = resolvedVersion
		s.pinnedPkgs[newKey] = entry
	}
	_ = s.savePinnedPackagesLocked()
}

// handlePinned handles GET/DELETE /api/pinned
func (s *server) handlePinned(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.pinnedMu.RLock()
		entries := make([]pinnedEntry, 0, len(s.pinnedPkgs))
		for _, e := range s.pinnedPkgs {
			entries = append(entries, e)
		}
		s.pinnedMu.RUnlock()
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Module+"@"+entries[i].Version < entries[j].Module+"@"+entries[j].Version
		})
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
		if err := s.unpinPackage(req.Module, req.Version); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *server) handleExportCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	incremental := r.URL.Query().Get("incremental") == "true"
	statePath := filepath.Join(s.cacheDir, ".export-state.json")

	state := make(map[string]bool)
	if incremental {
		stateBytes, err := os.ReadFile(statePath)
		if err == nil {
			var st struct {
				Files map[string]bool `json:"files"`
			}
			if err := json.Unmarshal(stateBytes, &st); err == nil && st.Files != nil {
				state = st.Files
			}
		}
	}

	newFiles := make(map[string]bool)

	if incremental {
		errFound := errors.New("found")
		hasNewFiles := false
		_ = filepath.Walk(s.cacheDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, relErr := filepath.Rel(s.cacheDir, path)
			if relErr != nil {
				return nil
			}
			relPath = filepath.ToSlash(relPath)
			if relPath == ".export-state.json" {
				return nil
			}
			if !state[relPath] {
				hasNewFiles = true
				return errFound
			}
			return nil
		})
		if !hasNewFiles {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	filename := fmt.Sprintf("go-offline-cache-%s.tar.gz", time.Now().Format("20060102-150405"))
	if incremental {
		filename = fmt.Sprintf("go-offline-cache-inc-%s.tar.gz", time.Now().Format("20060102-150405"))
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	baseDir := s.cacheDir
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Build a relative path inside the archive.
		relPath, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			return relErr
		}
		// Use forward slashes inside the tar.
		relPath = filepath.ToSlash(relPath)

		// Never export our internal state file.
		if relPath == ".export-state.json" {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Always include user-packages.json even in incremental exports,
		// because its content changes whenever packages are pinned/unpinned.
		if incremental && state[relPath] && relPath != "user-packages.json" {
			return nil
		}

		header, hErr := tar.FileInfoHeader(info, "")
		if hErr != nil {
			return hErr
		}
		header.Name = "cache/" + relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		f, fErr := os.Open(path)
		if fErr != nil {
			return fErr
		}
		defer f.Close()
		_, copyErr := io.Copy(tw, f)
		if copyErr == nil {
			newFiles[relPath] = true
		}
		return copyErr
	})
	if err != nil {
		log.Printf("error: export cache archive: %v", err)
	} else {
		// Update state on success
		if !incremental {
			state = newFiles
		} else {
			for k := range newFiles {
				state[k] = true
			}
		}
		stateBytes, _ := json.MarshalIndent(struct {
			Files map[string]bool `json:"files"`
		}{Files: state}, "", "  ")
		_ = os.WriteFile(statePath, stateBytes, 0644)
	}
}

func (s *server) handleImportCache(w http.ResponseWriter, r *http.Request) {
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

	gr, err := gzip.NewReader(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid gzip: " + err.Error()})
		return
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	extractedFiles := 0

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tar read error: " + err.Error()})
			return
		}

		// The export puts everything under "cache/", strip that prefix
		// so we extract directly into s.cacheDir.
		name := filepath.FromSlash(hdr.Name)
		name = strings.TrimPrefix(name, "cache")
		name = strings.TrimPrefix(name, string(filepath.Separator))
		if name == "" || name == "." {
			continue
		}

		target := filepath.Join(s.cacheDir, name)

		// Path traversal protection.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(s.cacheDir)+string(filepath.Separator)) {
			log.Printf("warn: import-cache skip path traversal: %s", hdr.Name)
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir: " + err.Error()})
				return
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir: " + err.Error()})
				return
			}
			f, fErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777))
			if fErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create file: " + fErr.Error()})
				return
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write file: " + err.Error()})
				return
			}
			f.Close()
			extractedFiles++
		}
	}

	log.Printf("import-cache: extracted %d files", extractedFiles)

	// Reload pinned packages from the (possibly updated) user-packages.json.
	if err := s.loadPinnedPackages(); err != nil {
		log.Printf("warn: reload pinned packages after import: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"extracted_files": extractedFiles,
		"message":         fmt.Sprintf("Импортировано %d файлов", extractedFiles),
	})
}

func (s *server) appendProxyLog(line string) {
	s.proxyLogsMu.Lock()
	s.proxyLogs = append(s.proxyLogs, line)
	if len(s.proxyLogs) > 500 {
		s.proxyLogs = s.proxyLogs[len(s.proxyLogs)-500:]
	}
	s.proxyLogsMu.Unlock()
}

func (s *server) snapshotProxyLogs(limit int) []string {
	s.proxyLogsMu.Lock()
	defer s.proxyLogsMu.Unlock()
	if limit <= 0 || limit > len(s.proxyLogs) {
		limit = len(s.proxyLogs)
	}
	start := len(s.proxyLogs) - limit
	return append([]string(nil), s.proxyLogs[start:]...)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) newBudget() *fetchBudget {
	return &fetchBudget{
		maxBytes:   s.maxJobBytes,
		maxModules: s.maxModules,
	}
}

func (b *fetchBudget) noteModule() error {
	b.modules++
	if b.maxModules > 0 && b.modules > b.maxModules {
		return fmt.Errorf("job limit reached: modules=%d > max=%d", b.modules, b.maxModules)
	}
	return nil
}

func (b *fetchBudget) noteBytes(n int64) error {
	b.bytes += n
	if b.maxBytes > 0 && b.bytes > b.maxBytes {
		return fmt.Errorf("job limit reached: downloaded=%s > max=%s", humanBytes(b.bytes), humanBytes(b.maxBytes))
	}
	return nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func (s *server) newJob(kind string) *jobState {
	id := fmt.Sprintf("j-%d", atomic.AddUint64(&s.jobSeq, 1))
	job := &jobState{
		ID:        id,
		Kind:      kind,
		State:     "queued",
		StartedAt: time.Now().Format(time.RFC3339),
		Logs:      make([]string, 0, 64),
	}
	job.logf("job created")
	s.jobsMu.Lock()
	s.jobs[id] = job
	s.jobsMu.Unlock()
	return job
}

func (s *server) getJob(id string) (*jobState, bool) {
	s.jobsMu.RLock()
	job, ok := s.jobs[id]
	s.jobsMu.RUnlock()
	return job, ok
}

func (j *jobState) logf(format string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.State == "queued" {
		j.State = "running"
	}
	line := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	j.Logs = append(j.Logs, line)
	if len(j.Logs) > 300 {
		j.Logs = j.Logs[len(j.Logs)-300:]
	}
	j.Message = fmt.Sprintf("logs=%d", len(j.Logs))
}

func (j *jobState) complete(report prefetchReport, msg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.State = "done"
	j.Message = msg
	j.Downloaded = report.Downloaded
	j.Skipped = report.Skipped
	j.FinishedAt = time.Now().Format(time.RFC3339)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+" job done: "+msg)
}

func (j *jobState) fail(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.State = "error"
	j.Error = err.Error()
	j.FinishedAt = time.Now().Format(time.RFC3339)
	j.Logs = append(j.Logs, time.Now().Format("15:04:05")+" job error: "+err.Error())
}

func (j *jobState) snapshot() *jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := &jobState{
		ID:         j.ID,
		Kind:       j.Kind,
		State:      j.State,
		Message:    j.Message,
		Error:      j.Error,
		StartedAt:  j.StartedAt,
		FinishedAt: j.FinishedAt,
		Downloaded: append([]string(nil), j.Downloaded...),
		Skipped:    append([]string(nil), j.Skipped...),
		Logs:       append([]string(nil), j.Logs...),
	}
	return cp
}

var singleRequireRE = regexp.MustCompile(`^require\s+(\S+)\s+(\S+)`)

func parseGoModRequires(content string) ([]modReq, error) {
	var (
		reqs    []modReq
		inBlock bool
	)
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "require (") {
			inBlock = true
			continue
		}
		if inBlock && line == ")" {
			inBlock = false
			continue
		}
		if inBlock {
			line = strings.Split(line, "//")[0]
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				path, err := decodeGoModToken(fields[0])
				if err != nil {
					return nil, err
				}
				ver, err := decodeGoModToken(fields[1])
				if err != nil {
					return nil, err
				}
				reqs = append(reqs, modReq{Path: path, Version: ver})
			}
			continue
		}
		m := singleRequireRE.FindStringSubmatch(line)
		if len(m) == 3 {
			path, err := decodeGoModToken(m[1])
			if err != nil {
				return nil, err
			}
			ver, err := decodeGoModToken(m[2])
			if err != nil {
				return nil, err
			}
			reqs = append(reqs, modReq{Path: path, Version: ver})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return reqs, nil
}

func decodeGoModToken(tok string) (string, error) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "", errors.New("empty token in go.mod")
	}
	if strings.HasPrefix(tok, `"`) && strings.HasSuffix(tok, `"`) {
		v, err := strconv.Unquote(tok)
		if err != nil {
			return "", fmt.Errorf("invalid quoted token %q: %w", tok, err)
		}
		return v, nil
	}
	return tok, nil
}

func escapeModulePath(s string) (string, error) {
	return escapeString(s, true)
}

func escapeModuleVersion(s string) (string, error) {
	return escapeString(s, false)
}

func escapeString(s string, isPath bool) (string, error) {
	if s == "" {
		return "", errors.New("empty value")
	}
	var b strings.Builder
	for _, r := range s {
		if r == '!' {
			b.WriteString("!!")
			continue
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("invalid character %q", r)
		}
		if !isPath && r == '/' {
			return "", errors.New("version must not contain slash")
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

func unescapeModulePath(s string) (string, error) {
	return unescapeString(s)
}

func unescapeModuleVersion(s string) (string, error) {
	return unescapeString(s)
}

func unescapeString(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '!' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", errors.New("invalid escape")
		}
		next := s[i+1]
		if next == '!' {
			b.WriteByte('!')
			i++
			continue
		}
		if next < 'a' || next > 'z' {
			return "", errors.New("invalid escaped letter")
		}
		b.WriteByte(next - 'a' + 'A')
		i++
	}
	return b.String(), nil
}
