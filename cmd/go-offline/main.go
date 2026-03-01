package main

import (
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
