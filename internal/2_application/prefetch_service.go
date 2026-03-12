package application

import (
	"context"
	"fmt"
	"strings"

	"go-offline/internal/1_domain/cache"
	"go-offline/internal/1_domain/prefetch"
)

type Resolver interface {
	ResolveVersionFromCache(modPath string) (string, error)
	ResolveModulePath(ctx context.Context, pkgPath, version string) (string, error)
}

type PrefetchService struct {
	downloader prefetch.Downloader
	jobsRepo   prefetch.JobRepository
	pinnedRepo cache.PinnedRepository
	resolver   Resolver
}

func NewPrefetchService(d prefetch.Downloader, j prefetch.JobRepository, p cache.PinnedRepository, r Resolver) *PrefetchService {
	return &PrefetchService{
		downloader: d,
		jobsRepo:   j,
		pinnedRepo: p,
		resolver:   r,
	}
}

func (s *PrefetchService) PrefetchModule(ctx context.Context, module, version string, recursive bool) (string, error) {
	// Let's resolve the module path to root path before we queue the job.
	if rMod, err := s.resolver.ResolveModulePath(ctx, module, version); err == nil && rMod != module {
		module = rMod
	}

	job := s.jobsRepo.Create("prefetch-module")
	job.Logf("start module=%s version=%s recursive=%t", module, version, recursive)

	if err := s.pinnedRepo.Pin(module, version); err != nil {
		job.Logf("warn: pin package: %v", err)
	}

	go func() {
		err := s.downloader.DownloadModule(context.Background(), module, version, recursive, job.Logf)
		if err != nil {
			job.Fail(err)
			return
		}
		if strings.TrimSpace(version) == "" {
			if resolved, err := s.resolver.ResolveVersionFromCache(module); err == nil && resolved != "" {
				job.Logf("resolved pinned version %s -> %s", module, resolved)
				s.pinnedRepo.ResolvePinnedLatest(module, resolved)
			}
		}
		job.Complete(prefetch.Report{}, "done via go tool")
	}()

	return job.Snapshot().ID, nil
}

func (s *PrefetchService) PrefetchGoMod(ctx context.Context, reqGoMod string, recursive bool, requires []struct{ Path, Version string }) (string, error) {
	job := s.jobsRepo.Create("prefetch-gomod")
	job.Logf("start requires=%d recursive=%t", len(requires), recursive)

	for _, r := range requires {
		if err := s.pinnedRepo.Pin(r.Path, r.Version); err != nil {
			job.Logf("warn: pin package %s@%s: %v", r.Path, r.Version, err)
		}
	}

	go func() {
		err := s.downloader.DownloadGoMod(context.Background(), reqGoMod, recursive, job.Logf)
		if err != nil {
			job.Fail(err)
			return
		}
		job.Complete(prefetch.Report{}, "done via go tool")
	}()

	return job.Snapshot().ID, nil
}

func (s *PrefetchService) GetJob(id string) (*prefetch.JobState, error) {
	job, ok := s.jobsRepo.Get(id)
	if !ok {
		return nil, fmt.Errorf("job not found")
	}
	return job, nil
}
