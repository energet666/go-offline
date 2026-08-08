package gotool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// maxDownloadBatch limits how many module@version arguments go into a single
// 'go mod download' call, so the command line stays well under the OS argument
// limit on very large module graphs.
const maxDownloadBatch = 100

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

// downloadedModule mirrors the JSON objects printed by 'go mod download -json'.
type downloadedModule struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Error   string `json:"Error,omitempty"`
}

// listedModule mirrors the JSON objects printed by 'go list -m -json'.
type listedModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Main    bool          `json:"Main"`
	Replace *listedModule `json:"Replace,omitempty"`
}

// editedGoMod mirrors the JSON printed by 'go mod edit -json'.
type editedGoMod struct {
	Require []struct {
		Path    string `json:"Path"`
		Version string `json:"Version"`
	} `json:"Require"`
	Replace []struct {
		Old struct {
			Path    string `json:"Path"`
			Version string `json:"Version"`
		} `json:"Old"`
		New struct {
			Path    string `json:"Path"`
			Version string `json:"Version"`
		} `json:"New"`
	} `json:"Replace"`
}

// DownloadModule downloads modPath@version into the cache and, when recursive
// is set, every module of its build list as well. It returns the version the
// request resolved to, which differs from version when it was empty (latest).
func (d *Downloader) DownloadModule(ctx context.Context, modPath, version string, recursive bool, logf func(string, ...any)) (string, error) {
	workdir, err := os.MkdirTemp(filepath.Join(d.workDir, "tmp"), "prefetch-module-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workdir)
	defer d.cleanupUnpackedModules(logf)

	target := modPath + "@" + valueOr(version, "latest")
	logf("go download target %s", target)

	if err := d.runGoCmd(ctx, workdir, logf, "mod", "init", "prefetch.local/job"); err != nil {
		return "", err
	}

	resolved, err := d.downloadTarget(ctx, workdir, logf, target)
	if err != nil {
		return "", err
	}
	if resolved.Version != version {
		logf("resolved %s -> %s@%s", target, resolved.Path, resolved.Version)
	}
	if !recursive {
		return resolved.Version, nil
	}

	// Write the requirement into go.mod directly instead of running 'go get'.
	// go get has to work out which module provides the path and probes every
	// parent prefix upstream, paying one guaranteed 404 per prefix.
	required := resolved.Path + "@" + resolved.Version
	if err := d.runGoCmd(ctx, workdir, logf, "mod", "edit", "-require="+required); err != nil {
		return resolved.Version, err
	}
	// Download again now that the module is a requirement: this is what writes
	// go.sum, without which 'go list -m' below refuses to run.
	if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", "-x", required); err != nil {
		return resolved.Version, err
	}

	return resolved.Version, d.downloadModuleGraph(ctx, workdir, logf)
}

// DownloadGoMod downloads the modules required by the given go.mod content.
// Without recursive only the requirements listed in the file are fetched; with
// it, the whole build list they resolve to.
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
	if err := d.dropLocalReplaces(ctx, workdir, logf); err != nil {
		return err
	}

	if !recursive {
		requires, err := d.directRequires(ctx, workdir, logf)
		if err != nil {
			return err
		}
		if len(requires) == 0 {
			logf("go.mod has no requirements to download")
			return nil
		}
		failed := d.downloadTargets(ctx, workdir, logf, requires)
		logf("download completed: attempted=%d failed=%d", len(requires), failed)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d modules failed to download", failed, len(requires))
		}
		return nil
	}

	// Downloading the direct requirements is also what writes go.sum, without
	// which the 'go list -m' inside downloadModuleGraph fails with
	// "missing go.sum entry for go.mod file".
	if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", "-x"); err != nil {
		return err
	}
	return d.downloadModuleGraph(ctx, workdir, logf)
}

// downloadTarget downloads a single module@version and reports what the query
// resolved to, so that a "@latest" request yields a concrete version.
func (d *Downloader) downloadTarget(ctx context.Context, workdir string, logf func(string, ...any), target string) (downloadedModule, error) {
	out, runErr := d.runGoCmdOutput(ctx, workdir, logf, "mod", "download", "-x", "-json", target)

	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var m downloadedModule
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m.Error != "" {
			return downloadedModule{}, fmt.Errorf("download %s: %s", target, firstLine(m.Error))
		}
		if m.Path != "" && m.Version != "" {
			return m, nil
		}
	}
	if runErr != nil {
		return downloadedModule{}, runErr
	}
	return downloadedModule{}, fmt.Errorf("go mod download reported no result for %s", target)
}

// downloadModuleGraph downloads every module in the build list of the module in
// workdir. An individual module failing is logged but does not abort the job:
// one unreachable module should not discard everything else that was fetched.
// Only cancellation is reported back, so that a cancelled job is not mistaken
// for a complete one.
func (d *Downloader) downloadModuleGraph(ctx context.Context, workdir string, logf func(string, ...any)) error {
	targets, err := d.buildList(ctx, workdir, logf)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logf("warn: unable to enumerate module graph: %v", err)
		return nil
	}
	if len(targets) == 0 {
		return nil
	}

	failed := d.downloadTargets(ctx, workdir, logf, targets)
	logf("graph download completed: attempted=%d failed=%d", len(targets), failed)
	if ctx.Err() != nil {
		return ctx.Err()
	}

	d.downloadRequirementMods(ctx, workdir, logf, targets)
	return ctx.Err()
}

// downloadRequirementMods caches the go.mod of every module reachable through a
// requirement edge, not just of the modules MVS selected. A consumer resolving
// imports walks edges that point at versions the build list does not contain —
// typically older ones required by pre-1.17 dependencies, whose requirements go
// expands transitively — and an offline build dies on the first one missing
// from the proxy.
//
// Requiring the whole build list directly is what forces go to load those
// requirement lists; 'go mod graph' then fetches every go.mod it walks into the
// cache. It rewrites go.mod, so this has to stay the last step of the job.
func (d *Downloader) downloadRequirementMods(ctx context.Context, workdir string, logf func(string, ...any), buildList []string) {
	for start := 0; start < len(buildList); start += maxDownloadBatch {
		batch := buildList[start:min(start+maxDownloadBatch, len(buildList))]
		args := make([]string, 0, len(batch)+2)
		args = append(args, "mod", "edit")
		for _, target := range batch {
			args = append(args, "-require="+target)
		}
		if err := d.runGoCmd(ctx, workdir, logf, args...); err != nil {
			logf("warn: unable to widen the module graph: %v", err)
			return
		}
	}

	logf("caching go.mod of every requirement edge")
	if _, err := d.runGoCmdOutput(ctx, workdir, logf, "mod", "graph"); err != nil {
		logf("warn: unable to walk the module graph: %v", err)
	}
}

// downloadTargets downloads modules in batches, because a single 'go mod
// download' fetches its arguments concurrently and is roughly twice as fast as
// a process per module. A failing batch is retried module by module, so one bad
// module is isolated instead of taking its whole batch down.
func (d *Downloader) downloadTargets(ctx context.Context, workdir string, logf func(string, ...any), targets []string) int {
	failed := 0
	for start := 0; start < len(targets); start += maxDownloadBatch {
		batch := targets[start:min(start+maxDownloadBatch, len(targets))]

		args := append([]string{"mod", "download", "-x"}, batch...)
		if err := d.runGoCmd(ctx, workdir, logf, args...); err == nil {
			continue
		}
		if ctx.Err() != nil {
			return failed + len(targets) - start
		}

		logf("warn: batch download failed, retrying %d modules one by one", len(batch))
		for i, target := range batch {
			if err := d.runGoCmd(ctx, workdir, logf, "mod", "download", "-x", target); err != nil {
				failed++
				logf("warn: skip %s: %v", target, err)
			}
			if ctx.Err() != nil {
				return failed + len(batch) - i - 1
			}
		}
	}
	return failed
}

// buildList returns the module@version of every module the main module in
// workdir resolves to, replacements applied.
func (d *Downloader) buildList(ctx context.Context, workdir string, logf func(string, ...any)) ([]string, error) {
	out, err := d.runGoCmdOutput(ctx, workdir, logf, "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}

	var targets []string
	seen := make(map[string]struct{})
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var m listedModule
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("unable to parse module graph: %w", err)
		}

		resolved := &m
		if m.Replace != nil {
			resolved = m.Replace
		}
		// A module replaced by a local directory has no version and nothing to
		// download.
		if resolved.Main || resolved.Version == "" {
			continue
		}

		target := resolved.Path + "@" + resolved.Version
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	return targets, nil
}

// directRequires returns the module@version of every require directive in the
// go.mod in workdir.
func (d *Downloader) directRequires(ctx context.Context, workdir string, logf func(string, ...any)) ([]string, error) {
	edited, err := d.readGoMod(ctx, workdir, logf)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(edited.Require))
	for _, req := range edited.Require {
		if req.Path == "" || req.Version == "" {
			continue
		}
		targets = append(targets, req.Path+"@"+req.Version)
	}
	return targets, nil
}

// dropLocalReplaces removes replace directives pointing at a filesystem path.
// Those directories only exist on the machine the go.mod came from, and go
// refuses to load the module graph without them; dropping them lets everything
// else in the file be prefetched from upstream.
func (d *Downloader) dropLocalReplaces(ctx context.Context, workdir string, logf func(string, ...any)) error {
	edited, err := d.readGoMod(ctx, workdir, logf)
	if err != nil {
		return err
	}
	for _, rep := range edited.Replace {
		if !isFilesystemPath(rep.New.Path) {
			continue
		}
		old := rep.Old.Path
		if rep.Old.Version != "" {
			old += "@" + rep.Old.Version
		}
		logf("warn: dropping replace %s => %s: local path is not available here", old, rep.New.Path)
		if err := d.runGoCmd(ctx, workdir, logf, "mod", "edit", "-dropreplace="+old); err != nil {
			return err
		}
	}
	return nil
}

func (d *Downloader) readGoMod(ctx context.Context, workdir string, logf func(string, ...any)) (editedGoMod, error) {
	out, err := d.runGoCmdOutput(ctx, workdir, logf, "mod", "edit", "-json")
	if err != nil {
		return editedGoMod{}, fmt.Errorf("unable to read go.mod: %w", err)
	}
	var edited editedGoMod
	if err := json.Unmarshal(out, &edited); err != nil {
		return editedGoMod{}, fmt.Errorf("unable to parse go.mod: %w", err)
	}
	return edited, nil
}

// runGoCmd runs a go command and streams everything it prints to logf.
func (d *Downloader) runGoCmd(ctx context.Context, workdir string, logf func(string, ...any), args ...string) error {
	_, err := d.run(ctx, workdir, logf, true, args...)
	return err
}

// runGoCmdOutput runs a go command and returns what it wrote to stdout. That
// output is machine-readable (JSON, graph edges) and is deliberately kept out
// of the log, which stays a readable trace of what the job is doing; stderr,
// where progress and diagnostics go, is still streamed to logf.
func (d *Downloader) runGoCmdOutput(ctx context.Context, workdir string, logf func(string, ...any), args ...string) ([]byte, error) {
	return d.run(ctx, workdir, logf, false, args...)
}

// run starts the go command and consumes both of its streams. Only stdout is
// captured: '-x' tracing goes to stderr and would otherwise corrupt the JSON
// that '-json' writes to stdout.
func (d *Downloader) run(ctx context.Context, workdir string, logf func(string, ...any), logStdout bool, args ...string) ([]byte, error) {
	logf("$ %s %s", d.goBin, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, d.goBin, args...)
	cmd.Dir = workdir
	cmd.Env = d.goEnv()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("command failed: %s %s: %w", d.goBin, strings.Join(args, " "), err)
	}

	// A capture per stream, so the two readers never touch the same memory.
	var out, errs streamCapture
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); out.read(stdout, true, logStdout, logf) }()
	go func() { defer wg.Done(); errs.read(stderr, false, true, logf) }()
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		// A cancelled job kills the process; report why it died rather than
		// the resulting signal.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out.buf, ctxErr
		}
		if out.needsToolchain || errs.needsToolchain {
			logf("hint: this go.mod requires a newer Go than the one installed here; update the go " +
				"binary (toolchains are deliberately not downloaded into the cache)")
		}
		// The interesting part of a go failure goes to stderr, so surface it
		// instead of a bare exit status.
		if errs.lastMessage != "" {
			return out.buf, fmt.Errorf("%s %s: %s", d.goBin, strings.Join(args[:min(2, len(args))], " "), errs.lastMessage)
		}
		return out.buf, fmt.Errorf("command failed: %s %s: %w", d.goBin, strings.Join(args, " "), err)
	}
	return out.buf, nil
}

// streamCapture consumes one output stream of a go command.
type streamCapture struct {
	buf            []byte
	lastMessage    string
	needsToolchain bool
}

func (c *streamCapture) read(r io.Reader, capture, log bool, logf func(string, ...any)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if capture {
			c.buf = append(c.buf, line...)
			c.buf = append(c.buf, '\n')
		}
		if strings.Contains(line, "GOTOOLCHAIN=local") {
			c.needsToolchain = true
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// '#' lines are -x request tracing, not diagnostics.
		if !strings.HasPrefix(trimmed, "#") {
			c.lastMessage = strings.TrimPrefix(trimmed, "go: ")
		}
		if log {
			logf("go: %s", trimmed)
		}
	}
}

func (d *Downloader) goEnv() []string {
	env := os.Environ()
	env = append(env, "GOMODCACHE="+filepath.Join(d.cacheDir, "gomodcache"))
	env = append(env, "GOCACHE="+filepath.Join(d.workDir, "gocache"))
	env = append(env, "GONOSUMDB=*")
	// Never let a go.mod with a newer go directive pull a toolchain: it is a
	// ~60MB zip for this machine's platform that would land in the exportable
	// cache, be invisible in the UI (no .info file) and be useless to an
	// offline machine on another platform.
	env = append(env, "GOTOOLCHAIN=local")
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

func isFilesystemPath(p string) bool {
	return p == "." || p == ".." ||
		strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") ||
		strings.HasPrefix(p, `.\`) || strings.HasPrefix(p, `..\`) ||
		filepath.IsAbs(p)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func valueOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}
