package gotool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go-offline/internal/1_domain/prefetch"
)

type downloader struct {
	goBin    string
	workDir  string
	cacheDir string
}

// New creates a new Downloader using the 'go' tool CLI.
func New(goBin, workDir, cacheDir string) prefetch.Downloader {
	return &downloader{
		goBin:    goBin,
		workDir:  workDir,
		cacheDir: cacheDir,
	}
}

func valueOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func (d *downloader) DownloadModule(ctx context.Context, modPath, version string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(d.workDir, "tmp"), "prefetch-module-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	target := modPath + "@" + valueOr(version, "latest")
	logf("go prefetch target %s", target)

	if err := d.runGoCmd(ctx, workdir, logf, "mod", "init", "prefetch.local/job"); err != nil {
		return err
	}

	if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
		return err
	}

	if recursive {
		if err := d.runGoCmd(ctx, workdir, logf, "get", target); err != nil {
			return err
		}
		if err := d.runGoCmd(ctx, workdir, logf, "mod", "download"); err != nil {
			return err
		}
		d.prefetchModuleGraphBestEffort(ctx, workdir, logf)
	}
	return nil
}

// Note: Requires fetching from a go.mod file, which the server uses parseGoModRequires for.
// We will let the Go tool do `go mod download` which natively parses go.mod.
// Wait, the original code parsed go.mod manually *if not recursive* and downloaded each require.
// If recursive, it just did `go mod download` and `prefetchModuleGraphBestEffort`.

func (d *downloader) DownloadGoMod(ctx context.Context, goMod string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(d.workDir, "tmp"), "prefetch-gomod-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	if err := os.WriteFile(filepath.Join(workdir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return err
	}

	if recursive {
		if err := d.runGoCmd(ctx, workdir, logf, "mod", "download"); err != nil {
			return err
		}
		d.prefetchModuleGraphBestEffort(ctx, workdir, logf)
		return nil
	}

	// This relies on `go list -m` parsing the local go.mod, which is native and doesn't require parseGoModRequires
	out, err := d.runGoCmdOutput(ctx, workdir, logf, "list", "-m", "-json", "all")
	if err != nil {
		return fmt.Errorf("unable to list modules: %v", err)
	}

	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var m struct {
			Path    string
			Version string
			Main    bool
		}
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			break
		}
		if m.Main || m.Version == "" {
			continue
		}
		target := m.Path + "@" + m.Version
		if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
			return err
		}
	}

	return nil
}

type listedModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Main    bool          `json:"Main"`
	Replace *listedModule `json:"Replace,omitempty"`
}

func (d *downloader) prefetchModuleGraphBestEffort(ctx context.Context, workdir string, logf func(string, ...any)) {
	out, err := d.runGoCmdOutput(ctx, workdir, logf, "list", "-m", "-json", "all")
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

		if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", target); err != nil {
			failed++
			logf("warn: skip %s: %v", target, err)
		}
	}
	if attempted > 0 {
		logf("graph prefetch completed: attempted=%d failed=%d", attempted, failed)
	}
}

func (d *downloader) runGoCmd(ctx context.Context, workdir string, logf func(string, ...any), args ...string) error {
	_, err := d.runGoCmdOutput(ctx, workdir, logf, args...)
	return err
}

func (d *downloader) runGoCmdOutput(ctx context.Context, workdir string, logf func(string, ...any), args ...string) ([]byte, error) {
	logf("$ %s %s", d.goBin, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, d.goBin, args...)
	cmd.Dir = workdir
	cmd.Env = d.goEnv()

	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) != "" {
				logf("go: %s", line)
			}
		}
	}
	if err != nil {
		return out, fmt.Errorf("command failed: %s %s: %w", d.goBin, strings.Join(args, " "), err)
	}
	return out, nil
}

func (d *downloader) goEnv() []string {
	env := os.Environ()
	env = append(env, "GOMODCACHE="+filepath.Join(d.cacheDir, "gomodcache"))
	env = append(env, "GOCACHE="+filepath.Join(d.workDir, "gocache"))
	env = append(env, "GONOSUMDB=*")
	return env
}
