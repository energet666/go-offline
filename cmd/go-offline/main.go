package main

import (
	"bufio"
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

const uiTemplate = `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>go-offline</title>
  <style>
    :root {
      --bg: #f5f7f2;
      --panel: #fffdf8;
      --line: #d9dfcf;
      --ink: #1f261b;
      --accent: #2f6a3f;
      --muted: #63705f;
    }
    body {
      margin: 0;
      font-family: "IBM Plex Sans", "Segoe UI", sans-serif;
      color: var(--ink);
      background: radial-gradient(circle at top, #e8f1dc, var(--bg));
    }
    .wrap {
      max-width: 1080px;
      margin: 28px auto;
      padding: 0 16px;
    }
    h1 {
      margin: 0 0 8px;
      font-size: 34px;
      letter-spacing: -0.02em;
    }
    p {
      color: var(--muted);
      margin: 0 0 14px;
    }
    .grid {
      display: grid;
      gap: 14px;
      grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    }
    .card {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 12px;
      padding: 14px;
      box-shadow: 0 6px 18px rgba(60, 75, 56, 0.08);
    }
    label {
      display: block;
      margin-top: 8px;
      font-size: 14px;
      color: var(--muted);
    }
    input, textarea {
      width: 100%;
      box-sizing: border-box;
      margin-top: 4px;
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 9px 10px;
      font-size: 14px;
      font-family: inherit;
      background: #fff;
    }
    textarea {
      min-height: 160px;
      font-family: "IBM Plex Mono", "SFMono-Regular", monospace;
    }
    .actions {
      margin-top: 12px;
      display: flex;
      gap: 8px;
      align-items: center;
    }
    button {
      border: 0;
      border-radius: 8px;
      padding: 10px 14px;
      font-weight: 600;
      color: #fff;
      background: var(--accent);
      cursor: pointer;
    }
    .status {
      color: var(--muted);
      font-size: 14px;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 14px;
    }
    th, td {
      text-align: left;
      border-bottom: 1px solid var(--line);
      padding: 8px 4px;
      vertical-align: top;
      word-break: break-word;
    }
    .chip {
      display: inline-block;
      font-size: 12px;
      color: #20522f;
      background: #d9ecda;
      border-radius: 999px;
      padding: 3px 8px;
    }
    .logbox {
      margin-top: 10px;
      background: #1d211a;
      color: #d8f2d0;
      border-radius: 8px;
      padding: 10px;
      min-height: 110px;
      max-height: 220px;
      overflow: auto;
      font-family: "IBM Plex Mono", "SFMono-Regular", monospace;
      font-size: 12px;
      white-space: pre-wrap;
    }
    .mini-help {
      margin: 12px 0 14px;
      background: #f2f8ec;
      border: 1px solid var(--line);
      border-radius: 10px;
      padding: 10px 12px;
    }
    .mini-help h3 {
      margin: 0 0 8px;
      font-size: 16px;
    }
    .mini-help pre {
      margin: 8px 0 0;
      background: #1d211a;
      color: #d8f2d0;
      border-radius: 8px;
      padding: 10px;
      overflow: auto;
      font-size: 12px;
      line-height: 1.45;
    }
  </style>
</head>
<body>
  <div class="wrap">
    <h1>go-offline</h1>
    <p>Локальный GOPROXY + prefetch зависимостей для офлайн-среды.</p>
    <div class="mini-help">
      <h3>Как подключить Go к этому прокси</h3>
      <p>Выполните один раз:</p>
      <pre>go env -w GOPROXY={{.ProxyURL}}
go env -w GOSUMDB=off</pre>
    </div>
    <div class="grid">
      <div class="card">
        <h3>Prefetch module@version</h3>
        <label>Module path
          <input id="module" placeholder="github.com/pkg/errors" />
        </label>
        <label>Version (optional)
          <input id="version" placeholder="v0.9.1 или пусто (= latest)" />
        </label>
        <label>
          <input id="recursive" type="checkbox" checked />
          Скачать рекурсивно зависимости из go.mod
        </label>
        <div class="actions">
          <button id="prefetchBtn">Скачать</button>
          <span id="prefetchStatus" class="status"></span>
        </div>
        <div id="prefetchLog" class="logbox">Логи появятся здесь.</div>
      </div>
      <div class="card">
        <h3>Prefetch из go.mod</h3>
        <label>Вставьте содержимое go.mod
          <textarea id="gomod" placeholder="module your/module&#10;go 1.22&#10;require github.com/pkg/errors v0.9.1"></textarea>
        </label>
        <label>
          <input id="gomodRecursive" type="checkbox" />
          Рекурсивно обходить зависимости из зависимостей (может быть долго)
        </label>
        <div class="actions">
          <button id="gomodBtn">Скачать зависимости</button>
          <span id="gomodStatus" class="status"></span>
        </div>
        <div id="gomodLog" class="logbox">Логи появятся здесь.</div>
      </div>
    </div>
    <div class="card" style="margin-top:14px">
      <h3>Кэшированные модули</h3>
      <p><span class="chip">GOPROXY={{.ProxyURL}}</span></p>
      <div class="actions" style="margin-bottom:8px">
        <input id="modulesQuery" placeholder="Поиск по module/version" />
        <button id="modulesSearchBtn" type="button">Найти</button>
      </div>
      <table id="modulesTable">
        <thead><tr><th>Module</th><th>Version</th><th>Time</th></tr></thead>
        <tbody></tbody>
      </table>
    </div>
  </div>
  <script>
    async function loadModules() {
      const q = document.getElementById('modulesQuery').value.trim();
      const url = q ? ('/api/modules?q=' + encodeURIComponent(q)) : '/api/modules';
      const res = await fetch(url);
      const rows = await res.json();
      const tbody = document.querySelector('#modulesTable tbody');
      tbody.innerHTML = '';
      rows.forEach(r => {
        const tr = document.createElement('tr');
        tr.innerHTML = '<td>' + r.module + '</td><td>' + r.version + '</td><td>' + (r.time || '') + '</td>';
        tbody.appendChild(tr);
      });
    }

    async function fetchJSON(url, options) {
      const res = await fetch(url, options);
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'request failed');
      return data;
    }

    function renderLogs(logEl, lines) {
      if (!lines || !lines.length) {
        logEl.textContent = 'Пока без логов...';
        return;
      }
      logEl.textContent = lines.join('\n');
      logEl.scrollTop = logEl.scrollHeight;
    }

    async function watchJob(jobId, statusEl, logEl) {
      let done = false;
      while (!done) {
        try {
          const job = await fetchJSON('/api/jobs/' + encodeURIComponent(jobId));
          const prefix = '[' + job.state + '] ';
          if (job.state === 'done') {
            statusEl.textContent = prefix + (job.message || 'Готово');
            done = true;
            await loadModules();
          } else if (job.state === 'error') {
            statusEl.textContent = prefix + (job.error || 'Ошибка');
            done = true;
          } else {
            statusEl.textContent = prefix + (job.message || 'Выполняется...');
          }
          renderLogs(logEl, job.logs || []);
        } catch (e) {
          statusEl.textContent = 'Ошибка статуса: ' + e.message;
          done = true;
        }
        if (!done) {
          await new Promise(r => setTimeout(r, 1000));
        }
      }
    }

    async function startJob(url, body, statusEl, logEl) {
      statusEl.textContent = 'Создание задачи...';
      logEl.textContent = 'Запуск...';
      try {
        const data = await fetchJSON(url, {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify(body)
        });
        statusEl.textContent = '[queued] Задача #' + data.job_id;
        await watchJob(data.job_id, statusEl, logEl);
      } catch (e) {
        statusEl.textContent = 'Ошибка: ' + e.message;
      }
    }

    document.getElementById('prefetchBtn').onclick = () => {
      startJob('/api/prefetch', {
        module: document.getElementById('module').value.trim(),
        version: document.getElementById('version').value.trim(),
        recursive: document.getElementById('recursive').checked
      }, document.getElementById('prefetchStatus'), document.getElementById('prefetchLog'));
    };

    document.getElementById('gomodBtn').onclick = () => {
      startJob('/api/prefetch-gomod', {
        gomod: document.getElementById('gomod').value,
        recursive: document.getElementById('gomodRecursive').checked
      }, document.getElementById('gomodStatus'), document.getElementById('gomodLog'));
    };

    document.getElementById('modulesSearchBtn').onclick = () => {
      loadModules();
    };
    document.getElementById('modulesQuery').onkeydown = (e) => {
      if (e.key === 'Enter') loadModules();
    };

    loadModules();
  </script>
</body>
</html>`

type server struct {
	cacheDir     string
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
}

type cachedModule struct {
	Module  string `json:"module"`
	Version string `json:"version"`
	Time    string `json:"time,omitempty"`
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
		cacheDir     = flag.String("cache", "./cache", "cache directory")
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

	if err := os.MkdirAll(filepath.Join(*cacheDir, "proxy"), 0o755); err != nil {
		log.Fatalf("create cache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*cacheDir, "gomodcache", "cache", "download"), 0o755); err != nil {
		log.Fatalf("create gomodcache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*cacheDir, "gocache"), 0o755); err != nil {
		log.Fatalf("create gocache dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*cacheDir, "tmp"), 0o755); err != nil {
		log.Fatalf("create tmp dir: %v", err)
	}

	s := &server{
		cacheDir: *cacheDir,
		upstream: strings.TrimRight(*upstream, "/"),
		httpClient: &http.Client{
			Timeout: *httpTimeout,
		},
		fetchRetries: *fetchRetries,
		retryBackoff: 2 * time.Second,
		goBin:        *goBin,
		maxJobBytes:  *maxJobBytes,
		maxModules:   *maxModules,
		tmpl:         template.Must(template.New("ui").Parse(uiTemplate)),
		jobs:         make(map[string]*jobState),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/modules", s.handleModules)
	mux.HandleFunc("/api/prefetch", s.handlePrefetch)
	mux.HandleFunc("/api/prefetch-gomod", s.handlePrefetchGoMod)
	mux.HandleFunc("/api/jobs/", s.handleJobStatus)

	log.Printf("go-offline started on %s", *listen)
	log.Printf("cache directory: %s", *cacheDir)
	log.Printf("upstream timeout: %s retries: %d", (*httpTimeout).String(), *fetchRetries)
	log.Printf("job limits: max-bytes=%d max-modules=%d", *maxJobBytes, *maxModules)
	log.Printf("go binary: %s", *goBin)
	log.Printf("set GOPROXY=http://127.0.0.1%s", *listen)
	if err := http.ListenAndServe(*listen, logRequests(mux)); err != nil {
		log.Fatal(err)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/":
		data := map[string]string{
			"ProxyURL": "http://" + r.Host,
		}
		_ = s.tmpl.Execute(w, data)
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

	job := s.newJob("prefetch-module")
	job.logf("start module=%s version=%s recursive=%t", req.Module, valueOr(req.Version, "latest"), req.Recursive)
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

	dir := filepath.Join(s.cacheDir, "proxy", filepath.FromSlash(escapedPath), "@v")
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
	workdir, err := os.MkdirTemp(filepath.Join(s.cacheDir, "tmp"), "prefetch-module-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	target := modPath + "@" + valueOr(version, "latest")
	logf("go prefetch target %s", target)

	if err := s.runGoCmd(ctx, workdir, logf, "mod", "init", "prefetch.local/job"); err != nil {
		return err
	}
	if err := s.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
		return err
	}
	if recursive {
		// go get adds module to go.mod; then download all pulls full module graph.
		if err := s.runGoCmd(ctx, workdir, logf, "get", target); err != nil {
			return err
		}
		if err := s.runGoCmd(ctx, workdir, logf, "mod", "download", "all"); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) prefetchGoModWithGo(ctx context.Context, goMod string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(s.cacheDir, "tmp"), "prefetch-gomod-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	if err := os.WriteFile(filepath.Join(workdir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return err
	}
	if recursive {
		return s.runGoCmd(ctx, workdir, logf, "mod", "download", "all")
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
		return fmt.Errorf("command failed: %s %s: %w", s.goBin, strings.Join(args, " "), err)
	}
	return nil
}

func (s *server) goEnv() []string {
	env := os.Environ()
	env = append(env, "GOMODCACHE="+filepath.Join(s.cacheDir, "gomodcache"))
	env = append(env, "GOCACHE="+filepath.Join(s.cacheDir, "gocache"))
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
	return filepath.Join(s.cacheDir, "proxy")
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
	target := filepath.Join(s.proxyBaseDir(), filepath.FromSlash(rel))
	if st, err := os.Stat(target); err == nil && !st.IsDir() {
		http.ServeFile(w, r, target)
		return
	}
	legacyTarget := filepath.Join(s.cacheDir, "proxy", filepath.FromSlash(rel))
	if st, err := os.Stat(legacyTarget); err == nil && !st.IsDir() {
		http.ServeFile(w, r, legacyTarget)
		return
	}

	http.NotFound(w, r)
}

func (s *server) listCachedModules(query string) ([]cachedModule, error) {
	base := s.proxyBaseDir()
	out := make([]cachedModule, 0, 128)
	query = strings.ToLower(strings.TrimSpace(query))

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
		row := cachedModule{
			Module:  modPath,
			Version: ver,
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
		if out[i].Module == out[j].Module {
			return out[i].Version < out[j].Version
		}
		return out[i].Module < out[j].Module
	})
	return out, nil
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

func (j *jobState) snapshot() jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := jobState{
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
