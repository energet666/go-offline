package httphandlers

import (
	"context"
	"html/template"
	"net/http"
	"sync"
	"time"

	"go-offline/internal/2_application"
	"go-offline/internal/1_domain/cache"
	"go-offline/internal/1_domain/prefetch"
)

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
	tmpl         *template.Template
	downloader   prefetch.Downloader
	prefetchSvc  *application.PrefetchService
	cacheSvc     *application.CacheService
	jobsRepo     prefetch.JobRepository
	proxyLogsMu  sync.Mutex
	proxyLogs    []string
	proxyLogSeq  uint64
	pinnedRepo   cache.PinnedRepository
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
	Tmpl         *template.Template
	Downloader   prefetch.Downloader
	CacheSvc     *application.CacheService
	JobsRepo     prefetch.JobRepository
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
		tmpl:         cfg.Tmpl,
		downloader:   cfg.Downloader,
		cacheSvc:     cfg.CacheSvc,
		jobsRepo:     cfg.JobsRepo,
		pinnedRepo:   cfg.PinnedRepo,
	}
}

func (s *Server) SetPrefetchService(svc *application.PrefetchService) {
	s.prefetchSvc = svc
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/modules", s.handleModules)
	mux.HandleFunc("/api/prefetch", s.handlePrefetch)
	mux.HandleFunc("/api/prefetch-gomod", s.handlePrefetchGoMod)
	mux.HandleFunc("/api/jobs/", s.handleJobStatus)
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
