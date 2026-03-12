package application

import (
	"fmt"
	"io"
	"sort"
	"time"

	"go-offline/internal/1_domain/cache"
)

type CacheService struct {
	cacheDir   string
	cacheRepo  cache.CacheRepository
	pinnedRepo cache.PinnedRepository
}

func NewCacheService(cacheRepo cache.CacheRepository, pinnedRepo cache.PinnedRepository) *CacheService {
	return &CacheService{
		cacheRepo:  cacheRepo,
		pinnedRepo: pinnedRepo,
	}
}

func (s *CacheService) ListCachedModules(query string) ([]cache.Module, error) {
	out, err := s.cacheRepo.ListCached(query)
	if err != nil {
		return nil, err
	}

	for i := range out {
		out[i].Pinned = s.pinnedRepo.IsPinned(out[i].Module, out[i].Version)
	}

	sort.Slice(out, func(i, j int) bool {
		// Pinned packages first, then alphabetical.
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if out[i].Module == out[j].Module {
			return out[i].Version < out[j].Version
		}
		return out[i].Module < out[j].Module
	})
	return out, nil
}

// ExportFilename returns a descriptive archive filename for the given export kind.
func ExportFilename(incremental bool) string {
	ts := time.Now().Format("2006-01-02_15-04-05")
	kind := "full"
	if incremental {
		kind = "incremental"
	}
	return fmt.Sprintf("go-offline-%s-%s.tar.gz", kind, ts)
}

func (s *CacheService) ExportCache(w io.Writer, incremental bool) error {
	return s.cacheRepo.Export(w, incremental)
}

func (s *CacheService) ImportCache(r io.Reader) (int, error) {
	n, err := s.cacheRepo.Import(r)
	if err != nil {
		return n, err
	}
	// Архив может содержать обновлённый user-packages.json —
	// перечитываем закреплённые пакеты из файла, чтобы in-memory состояние
	// соответствовало тому, что записано на диске.
	if reloadErr := s.pinnedRepo.Reload(); reloadErr != nil {
		return n, fmt.Errorf("imported %d files but failed to reload pinned packages: %w", n, reloadErr)
	}
	return n, nil
}
