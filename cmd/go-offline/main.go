package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	application "go-offline/internal/2_application"
	"go-offline/internal/3_infrastructure/fs_cache"
	"go-offline/internal/3_infrastructure/gotool"
	"go-offline/internal/3_infrastructure/inmem_jobs"
	httphandlers "go-offline/internal/4_presentation/http"
)

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

	if err := os.MkdirAll(filepath.Join(*cacheDir, "gomodcache", "cache", "download"), 0o755); err != nil {
		log.Fatalf("create gomodcache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*workDir, "proxy"), 0o755); err != nil {
		log.Fatalf("create proxy dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*workDir, "gocache"), 0o755); err != nil {
		log.Fatalf("create gocache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*workDir, "tmp"), 0o755); err != nil {
		log.Fatalf("create tmp dir: %v", err)
	}

	downloader := gotool.New(*goBin, *workDir, *cacheDir)
	jobsRepo := inmem_jobs.New()
	pinnedRepo, err := fs_cache.NewPinnedRepository(*cacheDir)
	if err != nil {
		log.Printf("warn: failed to initialize pinned packages: %v", err)
	}
	cacheRepo := fs_cache.NewCacheRepository(*cacheDir, *workDir)
	cacheSvc := application.NewCacheService(cacheRepo, pinnedRepo)

	srvCfg := httphandlers.ServerConfig{
		CacheDir:     *cacheDir,
		WorkDir:      *workDir,
		Upstream:     strings.TrimRight(*upstream, "/"),
		HttpClient:   &http.Client{Timeout: *httpTimeout},
		FetchRetries: *fetchRetries,
		RetryBackoff: 2 * time.Second,
		GoBin:        *goBin,
		MaxJobBytes:  *maxJobBytes,
		MaxModules:   *maxModules,
		Tmpl:         httphandlers.UITmpl,
		Downloader:   downloader,
		CacheSvc:     cacheSvc,
		JobsRepo:     jobsRepo,
		PinnedRepo:   pinnedRepo,
	}

	srv := httphandlers.NewServer(srvCfg)
	prefetchSvc := application.NewPrefetchService(downloader, jobsRepo, pinnedRepo, srv)
	srv.SetPrefetchService(prefetchSvc)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	log.Printf("go-offline started on %s", *listen)
	log.Printf("cache directory: %s", *cacheDir)
	log.Printf("work directory: %s", *workDir)
	log.Printf("upstream timeout: %s retries: %d", (*httpTimeout).String(), *fetchRetries)
	log.Printf("job limits: max-bytes=%d max-modules=%d", *maxJobBytes, *maxModules)
	log.Printf("go binary: %s", *goBin)
	log.Printf("set GOPROXY=http://127.0.0.1%s", *listen)

	if err := http.ListenAndServe(*listen, srv.Handler(mux)); err != nil {
		log.Fatal(err)
	}
}
