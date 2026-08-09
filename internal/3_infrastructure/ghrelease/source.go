// Package ghrelease читает манифест сборки, который CI кладёт рядом с бинарями
// в GitHub Releases, и качает оттуда бинарь под текущую платформу.
//
// Работа идёт через обычные ссылки на ассеты релиза, а не через GitHub API:
// они не требуют токена и не расходуют лимит в 60 анонимных запросов в час.
package ghrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"go-offline/internal/1_domain/selfupdate"
)

const (
	// manifestName — файл, который CI публикует рядом с бинарями.
	manifestName = "latest.json"
	// maxManifestSize защищает от чтения чего-то, что манифестом не является.
	maxManifestSize = 64 << 10
	// maxBinarySize ограничивает размер скачиваемого бинаря.
	maxBinarySize = 256 << 20
	// logEveryBytes — как часто сообщать о прогрессе скачивания.
	logEveryBytes = 2 << 20
)

// Source — источник сборок поверх статических ссылок релиза.
type Source struct {
	baseURL  string
	client   *http.Client
	platform string
}

// New создаёт источник, читающий манифест по адресу baseURL/latest.json.
func New(baseURL string, client *http.Client) *Source {
	return &Source{
		baseURL:  strings.TrimRight(baseURL, "/"),
		client:   client,
		platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
}

type manifestAsset struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Version string                   `json:"version"`
	BuiltAt string                   `json:"built_at"`
	Assets  map[string]manifestAsset `json:"assets"`
}

func (s *Source) Latest(ctx context.Context) (selfupdate.Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/"+manifestName, nil)
	if err != nil {
		return selfupdate.Release{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return selfupdate.Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return selfupdate.Release{}, fmt.Errorf("манифест недоступен: HTTP %d", resp.StatusCode)
	}

	var m manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifestSize)).Decode(&m); err != nil {
		return selfupdate.Release{}, fmt.Errorf("не удалось разобрать манифест: %w", err)
	}
	if m.Version == "" {
		return selfupdate.Release{}, fmt.Errorf("в манифесте нет версии")
	}

	rel := selfupdate.Release{
		Build: selfupdate.Build{Version: m.Version, BuiltAt: m.BuiltAt},
	}
	// Ассета под текущую платформу может не быть — это не ошибка проверки,
	// UI покажет версию и объяснит, почему обновиться нельзя.
	if a, ok := m.Assets[s.platform]; ok && a.File != "" {
		rel.AssetURL = s.baseURL + "/" + a.File
		rel.SHA256 = strings.ToLower(a.SHA256)
	}
	return rel, nil
}

func (s *Source) Download(ctx context.Context, rel selfupdate.Release, dir string, logf func(string, ...any)) (string, error) {
	if rel.AssetURL == "" {
		return "", selfupdate.ErrNoAsset
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.AssetURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("не удалось скачать %s: HTTP %d", rel.AssetURL, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(dir, ".go-offline-update-*")
	if err != nil {
		return "", fmt.Errorf("создать временный файл: %w", err)
	}
	tmpName := tmp.Name()
	// До успешного возврата файл считается мусором и удаляется.
	keep := false
	defer func() {
		tmp.Close()
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()

	logf("скачиваю %s", rel.AssetURL)
	hash := sha256.New()
	pw := &progressWriter{logf: logf}
	written, err := io.Copy(io.MultiWriter(tmp, hash, pw), io.LimitReader(resp.Body, maxBinarySize))
	if err != nil {
		return "", fmt.Errorf("скачивание прервано: %w", err)
	}
	if written == maxBinarySize {
		return "", fmt.Errorf("файл больше допустимых %d байт", int64(maxBinarySize))
	}
	if written == 0 {
		return "", fmt.Errorf("скачан пустой файл")
	}

	if rel.SHA256 != "" {
		got := hex.EncodeToString(hash.Sum(nil))
		if got != rel.SHA256 {
			return "", fmt.Errorf("контрольная сумма не совпала: ожидалось %s, получено %s", rel.SHA256, got)
		}
		logf("контрольная сумма совпала (%s…)", got[:12])
	}

	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("закрыть временный файл: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", fmt.Errorf("выставить права на файл: %w", err)
	}

	logf("скачано %.1f МБ в %s", float64(written)/(1<<20), filepath.Base(tmpName))
	keep = true
	return tmpName, nil
}

// progressWriter сообщает о прогрессе скачивания в лог задачи.
type progressWriter struct {
	logf     func(string, ...any)
	total    int64
	reported int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.total += int64(len(b))
	if p.total-p.reported >= logEveryBytes {
		p.reported = p.total
		p.logf("скачано %.1f МБ", float64(p.total)/(1<<20))
	}
	return len(b), nil
}
