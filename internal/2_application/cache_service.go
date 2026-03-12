package application

import (
	"errors"
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

func (s *CacheService) ExportCache(w io.Writer, incremental bool) (string, error) {
	filename := fmt.Sprintf("go-offline-cache-%s.tar.gz", time.Now().Format("20060102-150405"))
	if incremental {
		filename = fmt.Sprintf("go-offline-cache-inc-%s.tar.gz", time.Now().Format("20060102-150405"))
	}

	err := s.cacheRepo.Export(w, incremental)
	if err != nil {
		if errors.Is(err, cache.ErrNoNewFiles) {
			return "", nil // StatusNoContent equivalent
		}
		return filename, err
	}

	return filename, nil
}

func (s *CacheService) ImportCache(r io.Reader) (int, error) {
	return s.cacheRepo.Import(r)
}
