package main

import (
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-offline/internal/1_domain/selfupdate"
	"go-offline/internal/3_infrastructure/fs_cache"
	"go-offline/internal/3_infrastructure/ghrelease"
	"go-offline/internal/3_infrastructure/goproxy"
	"go-offline/internal/3_infrastructure/gotool"
	"go-offline/internal/3_infrastructure/selfinstall"
	httphandlers "go-offline/internal/4_presentation/http"
)

// defaultUpdateURL — база, откуда берутся манифест сборки и бинари.
// Тег скользящий: релиз nightly всегда указывает на свежий коммит main.
const defaultUpdateURL = "https://github.com/energet666/go-offline/releases/download/nightly"

// Подставляются линковщиком при сборке в CI: -ldflags "-X main.version=... -X main.buildTime=...".
// В локальной сборке остаются значениями по умолчанию.
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	var (
		listen      = flag.String("listen", ":8080", "HTTP listen address")
		cacheDir    = flag.String("cache", "./cache", "cache directory (persistent, for export/import)")
		workDir     = flag.String("workdir", "./workdir", "working directory (ephemeral: gocache, tmp)")
		upstream    = flag.String("upstream", "https://proxy.golang.org", "upstream GOPROXY")
		httpTimeout = flag.Duration("http-timeout", 5*time.Minute, "HTTP timeout for upstream requests")
		goBin       = flag.String("go-bin", "go", "path to go binary")
		updatesTTL  = flag.Duration("updates-ttl", 30*time.Minute, "how long to reuse the pinned-modules update check result")
		updateURL   = flag.String("update-url", defaultUpdateURL, "base URL with the published build manifest and binaries")
	)
	flag.Parse()

	// Бинарь, оставшийся от предыдущего обновления: на Windows удалить его
	// можно только после перезапуска.
	selfinstall.CleanupBackup()

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
	if err := os.MkdirAll(filepath.Join(*workDir, "gocache"), 0o755); err != nil {
		log.Fatalf("create gocache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*workDir, "tmp"), 0o755); err != nil {
		log.Fatalf("create tmp dir: %v", err)
	}

	_ = os.RemoveAll(filepath.Join(*workDir, "exports"))
	if err := os.MkdirAll(filepath.Join(*workDir, "exports"), 0o755); err != nil {
		log.Fatalf("create exports dir: %v", err)
	}

	downloader := gotool.New(*goBin, *workDir, *cacheDir)
	pinnedRepo, err := fs_cache.NewPinnedRepository(*cacheDir)
	if err != nil {
		log.Printf("warn: failed to initialize pinned packages: %v", err)
	}
	cacheRepo := fs_cache.NewCacheRepository(*cacheDir, *workDir)

	upstreamURL := strings.TrimRight(*upstream, "/")
	httpClient := &http.Client{Timeout: *httpTimeout}
	updateChecker := goproxy.NewUpdateChecker(upstreamURL, httpClient, *updatesTTL)

	// httpServer создаётся заранее: установщику нужен его Shutdown, чтобы
	// освободить порт до старта обновлённой копии.
	httpServer := &http.Server{Addr: *listen}
	releaseSource := ghrelease.New(*updateURL, httpClient)
	installer := selfinstall.New(httpServer.Shutdown)

	srv := httphandlers.NewServer(httphandlers.ServerConfig{
		CacheDir:      *cacheDir,
		WorkDir:       *workDir,
		Upstream:      upstreamURL,
		HttpClient:    httpClient,
		Downloader:    downloader,
		CacheRepo:     cacheRepo,
		PinnedRepo:    pinnedRepo,
		UpdateChecker: updateChecker,
		Build:         selfupdate.Build{Version: version, BuiltAt: buildTime},
		ReleaseSource: releaseSource,
		Installer:     installer,
	})

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	httpServer.Handler = srv.Handler(mux)

	log.Printf("go-offline %s (built %s) started on %s", version, buildTime, *listen)
	log.Printf("cache directory: %s", *cacheDir)
	log.Printf("work directory: %s", *workDir)
	log.Printf("upstream timeout: %s", (*httpTimeout).String())
	log.Printf("go binary: %s", *goBin)
	log.Printf("set GOPROXY=http://127.0.0.1%s", *listen)

	ln, err := listenTCP(*listen)
	if err != nil {
		log.Fatal(err)
	}
	err = httpServer.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		// Сервер гасит только установщик обновления — он же поднимает новую
		// копию и завершает процесс. Ждём этого, иначе main вернётся раньше
		// и убьёт перезапуск вместе с процессом.
		log.Printf("сервер остановлен для обновления, ожидаю старта новой версии")
		time.Sleep(selfinstall.RestartWindow)
		log.Fatal("новая версия не стартовала")
	}
	if err != nil {
		log.Fatal(err)
	}
}

// listenTCP занимает порт. Копия, поднятая обновлением, может застать порт ещё
// занятым предыдущей — на этот случай повторяем попытку несколько секунд.
func listenTCP(addr string) (net.Listener, error) {
	attempts := 1
	if os.Getenv(selfinstall.RestartedEnv) == "1" {
		attempts = 10
	}

	var lastErr error
	for i := range attempts {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
		if i < attempts-1 {
			log.Printf("порт %s занят, повтор через 500 мс", addr)
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil, lastErr
}
