package httphandlers

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go-offline/internal/1_domain/cache"
	application "go-offline/internal/2_application"
	"go-offline/internal/3_infrastructure/gotool"
)

// downloadState tracks the current background download operation.
type downloadState struct {
	mu         sync.Mutex
	Status     string   `json:"status"` // "idle", "running", "done", "error"
	Error      string   `json:"error,omitempty"`
	Message    string   `json:"message,omitempty"`
	Logs       []string `json:"logs"`
	StartedAt  string   `json:"started_at,omitempty"`
	FinishedAt string   `json:"finished_at,omitempty"`
}

func (ds *downloadState) logf(format string, args ...any) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	line := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	ds.Logs = append(ds.Logs, line)
	if len(ds.Logs) > 300 {
		ds.Logs = ds.Logs[len(ds.Logs)-300:]
	}
}

func (ds *downloadState) snapshot() downloadState {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return downloadState{
		Status:     ds.Status,
		Error:      ds.Error,
		Message:    ds.Message,
		Logs:       append([]string(nil), ds.Logs...),
		StartedAt:  ds.StartedAt,
		FinishedAt: ds.FinishedAt,
	}
}

type Server struct {
	cacheDir     string
	workDir      string
	upstream     string
	httpClient   *http.Client
	fetchRetries int
	retryBackoff time.Duration
	goBin        string
	maxJobBytes  int64
	maxModules   int64
	downloader   *gotool.Downloader
	cacheSvc     *application.CacheService
	pinnedRepo   cache.PinnedRepository
	proxyLogsMu  sync.Mutex
	proxyLogs    []string
	proxyLogSeq  uint64
	dlState      downloadState
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

type fetchBudget struct {
	maxBytes   int64
	maxModules int64
	bytes      int64
	modules    int64
}

type ServerConfig struct {
	CacheDir     string
	WorkDir      string
	Upstream     string
	HttpClient   *http.Client
	FetchRetries int
	RetryBackoff time.Duration
	GoBin        string
	MaxJobBytes  int64
	MaxModules   int64
	Downloader   *gotool.Downloader
	CacheSvc     *application.CacheService
	PinnedRepo   cache.PinnedRepository
}

func NewServer(cfg ServerConfig) *Server {
	return &Server{
		cacheDir:     cfg.CacheDir,
		workDir:      cfg.WorkDir,
		upstream:     cfg.Upstream,
		httpClient:   cfg.HttpClient,
		fetchRetries: cfg.FetchRetries,
		retryBackoff: cfg.RetryBackoff,
		goBin:        cfg.GoBin,
		maxJobBytes:  cfg.MaxJobBytes,
		maxModules:   cfg.MaxModules,
		downloader:   cfg.Downloader,
		cacheSvc:     cfg.CacheSvc,
		pinnedRepo:   cfg.PinnedRepo,
		dlState: downloadState{
			Status: "idle",
			Logs:   make([]string, 0),
		},
	}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/modules", s.handleModules)
	mux.HandleFunc("/api/prefetch", s.handlePrefetch)
	mux.HandleFunc("/api/prefetch-gomod", s.handlePrefetchGoMod)
	mux.HandleFunc("/api/download-status", s.handleDownloadStatus)
	mux.HandleFunc("/api/proxy-requests", s.handleProxyRequests)
	mux.HandleFunc("/api/pinned", s.handlePinned)
	mux.HandleFunc("/api/export-cache", s.handleExportCache)
	mux.HandleFunc("/api/import-cache", s.handleImportCache)
}

func (s *Server) ResolveVersionFromCache(modPath string) (string, error) {
	return s.resolveVersionFromCache(modPath)
}

func (s *Server) ResolveModulePath(ctx context.Context, pkgPath, version string) (string, error) {
	return s.resolveModulePath(ctx, pkgPath, version)
}

// Handler returns the HTTP handler with logging middleware
func (s *Server) Handler(mux *http.ServeMux) http.Handler {
	return s.logRequests(mux)
}
