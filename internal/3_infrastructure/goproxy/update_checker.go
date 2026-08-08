// Package goproxy содержит адаптеры, которые ходят в апстрим-GOPROXY по сети.
package goproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"go-offline/internal/1_domain/cache"
)

const (
	// maxMajorProbes ограничивает, насколько далеко вперёд ищем новые мажоры
	// (v2, v3, ...) — они живут по отдельным путям модуля.
	maxMajorProbes = 3
	// defaultWorkers — сколько модулей проверяем параллельно.
	defaultWorkers = 8
	// maxBodySize ограничивает размер ответа апстрима на /@latest.
	maxBodySize = 1 << 20
)

var errNotFound = errors.New("not found upstream")

// UpdateChecker спрашивает у апстрим-прокси последние версии закреплённых
// модулей и кэширует результат проверки на ttl.
type UpdateChecker struct {
	upstream string
	client   *http.Client
	ttl      time.Duration

	mu       sync.Mutex
	report   *cache.UpdatesReport
	reportAt time.Time
	// reportKey — отпечаток набора закреплённых модулей: если список изменился,
	// закэшированный отчёт больше не подходит.
	reportKey string
}

// NewUpdateChecker creates an update checker backed by the upstream GOPROXY.
func NewUpdateChecker(upstream string, client *http.Client, ttl time.Duration) *UpdateChecker {
	return &UpdateChecker{
		upstream: strings.TrimRight(upstream, "/"),
		client:   client,
		ttl:      ttl,
	}
}

func (c *UpdateChecker) CheckUpdates(ctx context.Context, entries []cache.PinnedEntry, force bool) (cache.UpdatesReport, error) {
	key := fingerprint(entries)

	if !force {
		c.mu.Lock()
		fresh := c.report != nil && c.reportKey == key && time.Since(c.reportAt) < c.ttl
		if fresh {
			rep := *c.report
			rep.Cached = true
			c.mu.Unlock()
			return rep, nil
		}
		c.mu.Unlock()
	}

	results := make([]cache.ModuleUpdate, len(entries))
	sem := make(chan struct{}, defaultWorkers)
	var wg sync.WaitGroup

	for i, e := range entries {
		wg.Add(1)
		go func(i int, e cache.PinnedEntry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = c.checkOne(ctx, e)
		}(i, e)
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return cache.UpdatesReport{}, err
	}

	report := cache.UpdatesReport{
		CheckedAt: time.Now().Format(time.RFC3339),
		Updates:   results,
	}

	c.mu.Lock()
	stored := report
	c.report = &stored
	c.reportAt = time.Now()
	c.reportKey = key
	c.mu.Unlock()

	return report, nil
}

func (c *UpdateChecker) checkOne(ctx context.Context, e cache.PinnedEntry) cache.ModuleUpdate {
	res := cache.ModuleUpdate{Module: e.Module, Version: e.Version}

	// Плейсхолдер "latest" ещё не разрешён в конкретную версию — сравнивать не с чем.
	if !semver.IsValid(e.Version) {
		res.Error = "version is not comparable"
		return res
	}

	info, err := c.fetchLatest(ctx, e.Module)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Latest = info.Version
	res.PublishedAt = info.Time
	if semver.Compare(info.Version, e.Version) > 0 {
		res.HasUpdate = true
	}

	if modPath, ver, ok := c.findNextMajor(ctx, e.Module, e.Version); ok {
		res.NextMajorModule = modPath
		res.NextMajorVersion = ver
		res.HasUpdate = true
	}
	return res
}

type latestInfo struct {
	Version string `json:"Version"`
	Time    string `json:"Time"`
}

func (c *UpdateChecker) fetchLatest(ctx context.Context, modPath string) (latestInfo, error) {
	escapedPath, err := module.EscapePath(modPath)
	if err != nil {
		return latestInfo{}, fmt.Errorf("bad module path: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.upstream+"/"+escapedPath+"/@latest", nil)
	if err != nil {
		return latestInfo{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return latestInfo{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusGone:
		return latestInfo{}, errNotFound
	default:
		return latestInfo{}, fmt.Errorf("upstream status %d", resp.StatusCode)
	}

	var info latestInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodySize)).Decode(&info); err != nil {
		return latestInfo{}, fmt.Errorf("bad upstream response: %w", err)
	}
	if info.Version == "" {
		return latestInfo{}, errors.New("upstream returned empty version")
	}
	return info, nil
}

// findNextMajor ищет мажорные версии, живущие по другому пути модуля
// (github.com/foo/bar -> github.com/foo/bar/v2), и возвращает самую свежую
// найденную. Про такие версии /@latest по текущему пути ничего не знает.
func (c *UpdateChecker) findNextMajor(ctx context.Context, modPath, version string) (string, string, bool) {
	base, major := splitMajor(modPath, version)
	var foundPath, foundVersion string

	for next := major + 1; next <= major+maxMajorProbes; next++ {
		candidate := base + "/v" + strconv.Itoa(next)
		info, err := c.fetchLatest(ctx, candidate)
		if err != nil {
			break
		}
		foundPath, foundVersion = candidate, info.Version
	}
	return foundPath, foundVersion, foundPath != ""
}

// splitMajor раскладывает путь модуля на базовую часть (без суффикса /vN) и
// номер текущего мажора. Для v0 и v1 суффикса нет, поэтому следующий мажор — v2.
func splitMajor(modPath, version string) (string, int) {
	if idx := strings.LastIndex(modPath, "/v"); idx >= 0 {
		if n, err := strconv.Atoi(modPath[idx+2:]); err == nil && n >= 2 {
			return modPath[:idx], n
		}
	}
	major := 1
	if n, err := strconv.Atoi(strings.TrimPrefix(semver.Major(version), "v")); err == nil && n > 1 {
		// v2+incompatible: мажор больше единицы, но суффикса в пути нет.
		major = n
	}
	return modPath, major
}

func fingerprint(entries []cache.PinnedEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Module)
		sb.WriteByte('@')
		sb.WriteString(e.Version)
		sb.WriteByte('\n')
	}
	return sb.String()
}
