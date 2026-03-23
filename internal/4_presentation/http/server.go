package httphandlers

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go-offline/internal/1_domain/cache"
	"go-offline/internal/3_infrastructure/gotool"
)

// downloadState tracks the current background download operation.
type downloadState struct {
	mu         sync.Mutex
	cancel     context.CancelFunc
	Status     string   `json:"status"` // "idle", "running", "done", "error"
	Error      string   `json:"error,omitempty"`
	Message    string   `json:"message,omitempty"`
	Logs       []string `json:"logs"`
	StartedAt  string   `json:"started_at,omitempty"`
	FinishedAt string   `json:"finished_at,omitempty"`
}

// downloadSnapshot is a mutex-free copy of downloadState for JSON serialization.
type downloadSnapshot struct {
	Status     string   `json:"status"`
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

func (ds *downloadState) snapshot() downloadSnapshot {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return downloadSnapshot{
		Status:     ds.Status,
		Error:      ds.Error,
		Message:    ds.Message,
		Logs:       append([]string(nil), ds.Logs...),
		StartedAt:  ds.StartedAt,
		FinishedAt: ds.FinishedAt,
	}
}

type Server struct {
	cacheDir    string
	workDir     string
	upstream    string
	httpClient  *http.Client
	downloader  *gotool.Downloader
	cacheRepo   cache.CacheRepository
	pinnedRepo  cache.PinnedRepository
	proxyLogsMu sync.Mutex
	proxyLogs   []string
	proxyLogSeq uint64
	dlState     downloadState
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

type ServerConfig struct {
	CacheDir   string
	WorkDir    string
	Upstream   string
	HttpClient *http.Client
	Downloader *gotool.Downloader
	CacheRepo  cache.CacheRepository
	PinnedRepo cache.PinnedRepository
}

func NewServer(cfg ServerConfig) *Server {
	return &Server{
		cacheDir:   cfg.CacheDir,
		workDir:    cfg.WorkDir,
		upstream:   cfg.Upstream,
		httpClient: cfg.HttpClient,
		downloader: cfg.Downloader,
		cacheRepo:  cfg.CacheRepo,
		pinnedRepo: cfg.PinnedRepo,
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
	mux.HandleFunc("/api/download-cancel", s.handleDownloadCancel)
	mux.HandleFunc("/api/proxy-requests", s.handleProxyRequests)
	mux.HandleFunc("/api/pinned", s.handlePinned)
	mux.HandleFunc("/api/export-cache/prepare", s.handleExportPrepare)
	mux.HandleFunc("/api/export-cache/download", s.handleExportDownload)
	mux.HandleFunc("/api/import-cache", s.handleImportCache)
}

// Handler returns the HTTP handler with logging middleware.
func (s *Server) Handler(mux *http.ServeMux) http.Handler {
	return s.logRequests(mux)
}

// proxyBaseDir returns the directory containing cached module files for proxy serving.
func (s *Server) proxyBaseDir() string {
	return s.cacheRepo.ProxyBaseDir()
}
