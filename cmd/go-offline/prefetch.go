package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

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

func (s *server) prefetchModuleWithGo(ctx context.Context, modPath, version string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(s.workDir, "tmp"), "prefetch-module-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	// The user may have entered a package path (e.g. golang.org/x/net/websocket)
	// instead of a module path (e.g. golang.org/x/net). Resolve to the actual
	// module path by probing the upstream proxy.
	resolvedModule, err := s.resolveModulePath(ctx, modPath, version)
	if err != nil {
		return fmt.Errorf("resolve module path for %q: %w", modPath, err)
	}
	if resolvedModule != modPath {
		logf("resolved package path %s -> module %s", modPath, resolvedModule)
		modPath = resolvedModule
	}

	target := modPath + "@" + valueOr(version, "latest")
	logf("go prefetch target %s", target)

	if err := s.runGoCmd(ctx, workdir, logf, "mod", "init", "prefetch.local/job"); err != nil {
		return err
	}
	if err := s.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
		return err
	}

	// If the user requested "latest" (no explicit version), resolve the real version
	// from the downloaded .info file and update the pinned entry accordingly.
	if strings.TrimSpace(version) == "" {
		if resolved, err := s.resolveVersionFromCache(modPath); err == nil && resolved != "" {
			logf("resolved pinned version %s -> %s", modPath, resolved)
			s.resolvePinnedLatest(modPath, resolved)
		}
	}

	if recursive {
		// go get adds module to go.mod; then mod download pulls required graph.
		// Avoid `download all` because it may fail on broken transitive/test-only revisions.
		if err := s.runGoCmd(ctx, workdir, logf, "get", target); err != nil {
			return err
		}
		if err := s.runGoCmd(ctx, workdir, logf, "mod", "download"); err != nil {
			return err
		}
		s.prefetchModuleGraphBestEffort(ctx, workdir, logf)
	}
	return nil
}

func (s *server) prefetchGoModWithGo(ctx context.Context, goMod string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(s.workDir, "tmp"), "prefetch-gomod-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	if err := os.WriteFile(filepath.Join(workdir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return err
	}
	if recursive {
		// Use `go mod download` instead of `download all` for better resilience when
		// dependency graphs contain unreachable historical revisions.
		if err := s.runGoCmd(ctx, workdir, logf, "mod", "download"); err != nil {
			return err
		}
		s.prefetchModuleGraphBestEffort(ctx, workdir, logf)
		return nil
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

func (s *server) prefetchModuleGraphBestEffort(ctx context.Context, workdir string, logf func(string, ...any)) {
	out, err := s.runGoCmdOutput(ctx, workdir, logf, "list", "-m", "-json", "all")
	if err != nil {
		logf("warn: unable to enumerate module graph: %v", err)
		return
	}

	dec := json.NewDecoder(strings.NewReader(string(out)))
	seen := make(map[string]struct{})
	attempted := 0
	failed := 0
	for {
		var m listedModule
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			logf("warn: unable to parse module graph: %v", err)
			return
		}

		resolved := &m
		if m.Replace != nil {
			resolved = m.Replace
		}
		if resolved.Main || resolved.Version == "" {
			continue
		}
		target := resolved.Path + "@" + resolved.Version
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		attempted++
		if err := s.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
			failed++
			logf("warn: skip %s: %v", target, err)
		}
	}
	if attempted > 0 {
		logf("graph prefetch completed: attempted=%d failed=%d", attempted, failed)
	}
}

func (s *server) runGoCmd(ctx context.Context, workdir string, logf func(string, ...any), args ...string) error {
	_, err := s.runGoCmdOutput(ctx, workdir, logf, args...)
	return err
}

func (s *server) runGoCmdOutput(ctx context.Context, workdir string, logf func(string, ...any), args ...string) ([]byte, error) {
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
		return out, fmt.Errorf("command failed: %s %s: %w", s.goBin, strings.Join(args, " "), err)
	}
	return out, nil
}

func (s *server) goEnv() []string {
	env := os.Environ()
	env = append(env, "GOMODCACHE="+filepath.Join(s.cacheDir, "gomodcache"))
	env = append(env, "GOCACHE="+filepath.Join(s.workDir, "gocache"))
	env = append(env, "GONOSUMDB=*")
	return env
}
