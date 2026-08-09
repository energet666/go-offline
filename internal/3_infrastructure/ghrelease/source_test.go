package ghrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go-offline/internal/1_domain/selfupdate"
)

// newTestSource поднимает локальный «релиз»: манифест и один бинарь.
func newTestSource(t *testing.T, manifest string, binary []byte) *Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			fmt.Fprint(w, manifest)
		case "/go-offline":
			w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, srv.Client())
}

func manifestFor(platform, sha string) string {
	return fmt.Sprintf(`{"version":"abc123","built_at":"2026-08-09T10:00:00Z",
		"assets":{%q:{"file":"go-offline","sha256":%q}}}`, platform, sha)
}

func TestLatestPicksAssetForCurrentPlatform(t *testing.T) {
	here := runtime.GOOS + "/" + runtime.GOARCH
	s := newTestSource(t, manifestFor(here, "deadbeef"), nil)

	rel, err := s.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}
	if rel.Version != "abc123" || rel.BuiltAt != "2026-08-09T10:00:00Z" {
		t.Errorf("получено %+v, ожидалась сборка abc123", rel.Build)
	}
	if !strings.HasSuffix(rel.AssetURL, "/go-offline") {
		t.Errorf("AssetURL = %q, ожидалась ссылка на бинарь", rel.AssetURL)
	}
	if rel.SHA256 != "deadbeef" {
		t.Errorf("SHA256 = %q, ожидалось deadbeef", rel.SHA256)
	}
}

// Релиз без сборки под текущую платформу — не ошибка проверки: версия
// известна, просто обновиться нечем.
func TestLatestWithoutAssetForPlatform(t *testing.T) {
	s := newTestSource(t, manifestFor("plan9/sparc", "deadbeef"), nil)

	rel, err := s.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}
	if rel.Version != "abc123" {
		t.Errorf("Version = %q, ожидалось abc123", rel.Version)
	}
	if rel.AssetURL != "" {
		t.Errorf("AssetURL = %q, ожидалась пустая строка", rel.AssetURL)
	}
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	payload := []byte("это как бы новый бинарь")
	sum := sha256.Sum256(payload)
	good := hex.EncodeToString(sum[:])
	here := runtime.GOOS + "/" + runtime.GOARCH

	t.Run("сумма совпала", func(t *testing.T) {
		s := newTestSource(t, manifestFor(here, good), payload)
		rel, err := s.Latest(context.Background())
		if err != nil {
			t.Fatalf("Latest() error: %v", err)
		}

		dir := t.TempDir()
		path, err := s.Download(context.Background(), rel, dir, func(string, ...any) {})
		if err != nil {
			t.Fatalf("Download() error: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("не удалось прочитать скачанный файл: %v", err)
		}
		if string(got) != string(payload) {
			t.Errorf("содержимое = %q, ожидалось %q", got, payload)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
			t.Errorf("права %v, ожидался бит выполнения", info.Mode().Perm())
		}
	})

	t.Run("сумма не совпала", func(t *testing.T) {
		s := newTestSource(t, manifestFor(here, strings.Repeat("0", 64)), payload)
		rel, err := s.Latest(context.Background())
		if err != nil {
			t.Fatalf("Latest() error: %v", err)
		}

		dir := t.TempDir()
		if _, err := s.Download(context.Background(), rel, dir, func(string, ...any) {}); err == nil {
			t.Fatal("ожидалась ошибка контрольной суммы, получен nil")
		}

		// Битый файл не должен остаться в каталоге рядом с исполняемым.
		left, err := filepath.Glob(filepath.Join(dir, "*"))
		if err != nil {
			t.Fatalf("glob: %v", err)
		}
		if len(left) != 0 {
			t.Errorf("в каталоге остались файлы: %v", left)
		}
	})
}

func TestDownloadWithoutAsset(t *testing.T) {
	s := newTestSource(t, manifestFor("plan9/sparc", "deadbeef"), nil)
	rel, err := s.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}

	_, err = s.Download(context.Background(), rel, t.TempDir(), func(string, ...any) {})
	if !errors.Is(err, selfupdate.ErrNoAsset) {
		t.Errorf("получена ошибка %v, ожидалась ErrNoAsset", err)
	}
}
