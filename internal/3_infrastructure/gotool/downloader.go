package gotool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Downloader wraps the 'go' CLI tool for downloading modules.
type Downloader struct {
	goBin    string
	workDir  string
	cacheDir string
}

// New creates a new Downloader using the 'go' tool CLI.
func New(goBin, workDir, cacheDir string) *Downloader {
	return &Downloader{
		goBin:    goBin,
		workDir:  workDir,
		cacheDir: cacheDir,
	}
}

func (d *Downloader) DownloadModule(ctx context.Context, modPath, version string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(d.workDir, "tmp"), "prefetch-module-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)
	defer d.cleanupUnpackedModules(logf)

	target := modPath + "@" + valueOr(version, "latest")
	logf("go download target %s", target)

	if err := d.runGoCmd(ctx, workdir, logf, "mod", "init", "prefetch.local/job"); err != nil {
		return err
	}

	if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", "-x", target); err != nil {
		return err
	}

	if recursive {
		if err := d.runGoCmd(ctx, workdir, logf, "get", "-x", target); err != nil {
			return err
		}
		if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", "-x"); err != nil {
			return err
		}
		d.downloadModuleGraphBestEffort(ctx, workdir, logf)
	}
	return nil
}

func (d *Downloader) DownloadGoMod(ctx context.Context, goMod string, recursive bool, logf func(string, ...any)) error {
	workdir, err := os.MkdirTemp(filepath.Join(d.workDir, "tmp"), "prefetch-gomod-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)
	defer d.cleanupUnpackedModules(logf)

	if err := os.WriteFile(filepath.Join(workdir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return err
	}

	if recursive {
		if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", "-x"); err != nil {
			return err
		}
		d.downloadModuleGraphBestEffort(ctx, workdir, logf)
		return nil
	}

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
		if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", "-x", target); err != nil {
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

func (d *Downloader) downloadModuleGraphBestEffort(ctx context.Context, workdir string, logf func(string, ...any)) {
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

		if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", "-x", target); err != nil {
			failed++
			logf("warn: skip %s: %v", target, err)
		}
	}
	if attempted > 0 {
		logf("graph download completed: attempted=%d failed=%d", attempted, failed)
	}
}

func (d *Downloader) runGoCmd(ctx context.Context, workdir string, logf func(string, ...any), args ...string) error {
	_, err := d.runGoCmdOutput(ctx, workdir, logf, args...)
	return err
}

// runGoCmdOutput runs a go command and streams its output line-by-line to logf
// in real-time, while also capturing the full output for return.
func (d *Downloader) runGoCmdOutput(ctx context.Context, workdir string, logf func(string, ...any), args ...string) ([]byte, error) {
	logf("$ %s %s", d.goBin, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, d.goBin, args...)
	cmd.Dir = workdir
	cmd.Env = d.goEnv()

	// Use a pipe to stream output line-by-line to logf as it arrives.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	var captured []byte
	streamDone := make(chan struct{})

	go func() {
		defer close(streamDone)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			captured = append(captured, line...)
			captured = append(captured, '\n')
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				logf("go: %s", trimmed)
			}
		}
	}()

	err := cmd.Run()
	pw.Close()
	<-streamDone

	if err != nil {
		return captured, fmt.Errorf("command failed: %s %s: %w", d.goBin, strings.Join(args, " "), err)
	}
	return captured, nil
}

func (d *Downloader) goEnv() []string {
	env := os.Environ()
	env = append(env, "GOMODCACHE="+filepath.Join(d.cacheDir, "gomodcache"))
	env = append(env, "GOCACHE="+filepath.Join(d.workDir, "gocache"))
	env = append(env, "GONOSUMDB=*")
	return env
}

// cleanupUnpackedModules removes unpacked module source directories from
// GOMODCACHE, keeping only the "cache" subdirectory which contains the
// .info, .mod, .zip files needed by the proxy.
func (d *Downloader) cleanupUnpackedModules(logf func(string, ...any)) {
	gomodcache := filepath.Join(d.cacheDir, "gomodcache")
	entries, err := os.ReadDir(gomodcache)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "cache" {
			continue
		}
		path := filepath.Join(gomodcache, entry.Name())
		_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				_ = os.Chmod(p, 0o755)
			} else {
				_ = os.Chmod(p, 0o644)
			}
			return nil
		})
		if err := os.RemoveAll(path); err != nil {
			logf("warn: cleanup unpacked module %s: %v", entry.Name(), err)
		} else {
			logf("cleanup: removed unpacked module directory %s", entry.Name())
		}
	}
}

func valueOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}
