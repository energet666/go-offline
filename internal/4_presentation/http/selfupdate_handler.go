package httphandlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"go-offline/internal/1_domain/selfupdate"
)

// checkReleaseTimeout ограничивает поход за манифестом сборки.
const checkReleaseTimeout = 30 * time.Second

// handleVersion отдаёт версию работающего бинаря. Никуда не ходит по сети,
// поэтому UI может дёргать его, чтобы поймать момент перезапуска.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, s.build)
}

// handleSelfUpdateCheck сравнивает текущую сборку с последней опубликованной.
// Требует доступа в интернет, поэтому вызывается только по кнопке.
func (s *Server) handleSelfUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.releaseSource == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "обновление не настроено"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), checkReleaseTimeout)
	defer cancel()

	rel, err := s.releaseSource.Latest(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	latest := rel.Build
	status := selfupdate.Status{
		Current:   s.build,
		Latest:    &latest,
		HasUpdate: rel.Version != s.build.Version,
	}

	switch {
	case !status.HasUpdate:
		status.Reason = "установлена последняя версия"
	case rel.AssetURL == "":
		status.Reason = fmt.Sprintf("в релизе нет сборки для %s/%s", runtime.GOOS, runtime.GOARCH)
	case s.installer == nil:
		status.Reason = "обновление не настроено"
	default:
		status.CanUpdate = true
		if s.build.IsDev() {
			status.Reason = "текущая сборка локальная (dev), она будет заменена сборкой из CI"
		}
	}

	writeJSON(w, http.StatusOK, status)
}

// handleSelfUpdateApply скачивает новую сборку и подменяет ею текущий бинарь.
// Идёт через общий слот фоновых задач: обновляться посреди prefetch нельзя —
// перезапуск оборвал бы скачивание модулей на полпути.
func (s *Server) handleSelfUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.releaseSource == nil || s.installer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "обновление не настроено"})
		return
	}

	err := s.startDownload("обновление приложения", func(ctx context.Context, logf func(string, ...any)) error {
		rel, err := s.releaseSource.Latest(ctx)
		if err != nil {
			return err
		}
		if rel.Version == s.build.Version {
			return selfupdate.ErrUpToDate
		}
		if rel.AssetURL == "" {
			return fmt.Errorf("%w: %s/%s", selfupdate.ErrNoAsset, runtime.GOOS, runtime.GOARCH)
		}
		logf("новая версия %s (собрана %s)", shortVersion(rel.Version), rel.BuiltAt)

		dir, err := s.installer.StageDir()
		if err != nil {
			return err
		}
		// Качаем рядом с исполняемым файлом: подмена должна быть
		// переименованием в пределах одной файловой системы.
		binary, err := s.releaseSource.Download(ctx, rel, dir, logf)
		if err != nil {
			return err
		}

		if err := s.installer.Apply(ctx, binary, logf); err != nil {
			_ = os.Remove(binary)
			return err
		}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// shortVersion укорачивает commit sha до привычных семи символов.
func shortVersion(v string) string {
	if len(v) > 7 {
		return v[:7]
	}
	return v
}
