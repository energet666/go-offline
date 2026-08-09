package httphandlers

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go-offline/internal/1_domain/cache"
	"go-offline/internal/1_domain/selfupdate"
	"go-offline/internal/3_infrastructure/gotool"
)

const (
	// maxLogLines caps how much of a job's output is kept in memory: '-x'
	// tracing is verbose enough that a large module graph produces tens of
	// thousands of lines.
	maxLogLines = 1000
	// headLogLines are never dropped, so the beginning of the job — target,
	// resolved version, first commands — stays visible however long it runs.
	headLogLines = 200
)

// downloadState tracks the current background download operation.
type downloadState struct {
	mu          sync.Mutex
	cancel      context.CancelFunc
	droppedLogs int
	Status      string   `json:"status"` // "idle", "running", "done", "error"
	Error       string   `json:"error,omitempty"`
	Message     string   `json:"message,omitempty"`
	Logs        []string `json:"logs"`
	StartedAt   string   `json:"started_at,omitempty"`
	FinishedAt  string   `json:"finished_at,omitempty"`
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
	if len(ds.Logs) > maxLogLines {
		// Drop from the middle: the head explains what the job is doing, the
		// tail is where it currently is.
		drop := len(ds.Logs) - maxLogLines
		ds.droppedLogs += drop
		ds.Logs = append(ds.Logs[:headLogLines], ds.Logs[headLogLines+drop:]...)
	}
}

func (ds *downloadState) snapshot() downloadSnapshot {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	logs := make([]string, 0, len(ds.Logs)+1)
	if ds.droppedLogs > 0 && len(ds.Logs) > headLogLines {
		logs = append(logs, ds.Logs[:headLogLines]...)
		logs = append(logs, fmt.Sprintf("… пропущено строк: %d …", ds.droppedLogs))
		logs = append(logs, ds.Logs[headLogLines:]...)
	} else {
		logs = append(logs, ds.Logs...)
	}

	return downloadSnapshot{
		Status:     ds.Status,
		Error:      ds.Error,
		Message:    ds.Message,
		Logs:       logs,
		StartedAt:  ds.StartedAt,
		FinishedAt: ds.FinishedAt,
	}
}

type Server struct {
	cacheDir      string
	workDir       string
	upstream      string
	httpClient    *http.Client
	downloader    *gotool.Downloader
	cacheRepo     cache.CacheRepository
	pinnedRepo    cache.PinnedRepository
	updateChecker cache.UpdateChecker
	build         selfupdate.Build
	releaseSource selfupdate.ReleaseSource
	installer     selfupdate.Installer
	proxyLogsMu   sync.Mutex
	proxyLogs     []string
	proxyLogSeq   uint64
	dlState       downloadState
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
	CacheDir      string
	WorkDir       string
	Upstream      string
	HttpClient    *http.Client
	Downloader    *gotool.Downloader
	CacheRepo     cache.CacheRepository
	PinnedRepo    cache.PinnedRepository
	UpdateChecker cache.UpdateChecker
	// Build, ReleaseSource и Installer отвечают за обновление самого
	// приложения. Пустые ReleaseSource/Installer просто выключают фичу.
	Build         selfupdate.Build
	ReleaseSource selfupdate.ReleaseSource
	Installer     selfupdate.Installer
}

func NewServer(cfg ServerConfig) *Server {
	return &Server{
		cacheDir:      cfg.CacheDir,
		workDir:       cfg.WorkDir,
		upstream:      cfg.Upstream,
		httpClient:    cfg.HttpClient,
		downloader:    cfg.Downloader,
		cacheRepo:     cfg.CacheRepo,
		pinnedRepo:    cfg.PinnedRepo,
		updateChecker: cfg.UpdateChecker,
		build:         cfg.Build,
		releaseSource: cfg.ReleaseSource,
		installer:     cfg.Installer,
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
	mux.HandleFunc("/api/pinned/updates", s.handlePinnedUpdates)
	mux.HandleFunc("/api/export-cache/prepare", s.handleExportPrepare)
	mux.HandleFunc("/api/export-cache/download", s.handleExportDownload)
	mux.HandleFunc("/api/import-cache", s.handleImportCache)
	mux.HandleFunc("/api/version", s.handleVersion)
	mux.HandleFunc("/api/self-update/check", s.handleSelfUpdateCheck)
	mux.HandleFunc("/api/self-update/apply", s.handleSelfUpdateApply)
}

// Handler returns the HTTP handler with logging middleware.
func (s *Server) Handler(mux *http.ServeMux) http.Handler {
	return s.logRequests(mux)
}

// proxyBaseDir returns the directory containing cached module files for proxy serving.
func (s *Server) proxyBaseDir() string {
	return s.cacheRepo.ProxyBaseDir()
}
