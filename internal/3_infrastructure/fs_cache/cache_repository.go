package fs_cache

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-offline/internal/1_domain/cache"

	"golang.org/x/mod/module"
)

type cacheRepository struct {
	cacheDir string
	workDir  string
}

// NewCacheRepository creates a new file-system backed repository for cached modules.
func NewCacheRepository(cacheDir, workDir string) cache.CacheRepository {
	return &cacheRepository{
		cacheDir: cacheDir,
		workDir:  workDir,
	}
}

func (r *cacheRepository) proxyBaseDir() string {
	newBase := filepath.Join(r.cacheDir, "gomodcache", "cache", "download")
	if st, err := os.Stat(newBase); err == nil && st.IsDir() {
		return newBase
	}
	return filepath.Join(r.workDir, "proxy")
}

func (r *cacheRepository) ListCached(query string) ([]cache.Module, int, error) {
	base := r.proxyBaseDir()
	out := make([]cache.Module, 0, 128)
	totalUnexported := 0
	query = strings.ToLower(strings.TrimSpace(query))

	exportStatePath := filepath.Join(r.cacheDir, ".export-state.json")
	exportedPaths := make(map[string]bool)
	if stateBytes, err := os.ReadFile(exportStatePath); err == nil {
		var st struct {
			Files map[string]bool `json:"files"`
		}
		if err := json.Unmarshal(stateBytes, &st); err == nil && st.Files != nil {
			exportedPaths = st.Files
		}
	}

	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".info") {
			return nil
		}

		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 3 || parts[len(parts)-2] != "@v" {
			return nil
		}

		escapedMod := strings.Join(parts[:len(parts)-2], "/")
		modPath, err := module.UnescapePath(escapedMod)
		if err != nil {
			modPath = escapedMod
		}

		escapedVer := strings.TrimSuffix(parts[len(parts)-1], ".info")
		ver, err := module.UnescapeVersion(escapedVer)
		if err != nil {
			ver = escapedVer
		}

		var info struct {
			Time time.Time `json:"Time"`
		}
		if data, readErr := os.ReadFile(path); readErr == nil {
			_ = json.Unmarshal(data, &info)
		}

		relFromCache := filepath.ToSlash(filepath.Join("gomodcache", "cache", "download", rel))

		row := cache.Module{
			Module:   modPath,
			Version:  ver,
			Exported: exportedPaths[relFromCache],
			// Pinned flag will be populated by the application service
		}
		if !row.Exported {
			totalUnexported++
		}
		if !info.Time.IsZero() {
			row.Time = info.Time.Format(time.RFC3339)
		}
		if query != "" {
			moduleLC := strings.ToLower(row.Module)
			versionLC := strings.ToLower(row.Version)
			if !strings.Contains(moduleLC, query) && !strings.Contains(versionLC, query) {
				return nil
			}
		}
		out = append(out, row)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	// Default sort; application service will re-sort specifically for pinned modules
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module == out[j].Module {
			return out[i].Version < out[j].Version
		}
		return out[i].Module < out[j].Module
	})
	return out, totalUnexported, nil
}

func (r *cacheRepository) Export(w io.Writer, incremental bool) error {
	statePath := filepath.Join(r.cacheDir, ".export-state.json")
	state := make(map[string]bool)

	if incremental {
		stateBytes, err := os.ReadFile(statePath)
		if err == nil {
			var st struct {
				Files map[string]bool `json:"files"`
			}
			if err := json.Unmarshal(stateBytes, &st); err == nil && st.Files != nil {
				state = st.Files
			}
		}
	}

	newFiles := make(map[string]bool)

	if incremental {
		errFound := errors.New("found")
		hasNewFiles := false
		_ = filepath.Walk(r.cacheDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, relErr := filepath.Rel(r.cacheDir, path)
			if relErr != nil {
				return nil
			}
			relPath = filepath.ToSlash(relPath)
			if relPath == ".export-state.json" {
				return nil
			}
			if !state[relPath] {
				hasNewFiles = true
				return errFound
			}
			return nil
		})
		if !hasNewFiles {
			return cache.ErrNoNewFiles
		}
	}

	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	baseDir := r.cacheDir
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Build a relative path inside the archive.
		relPath, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			return relErr
		}
		// Use forward slashes inside the tar.
		relPath = filepath.ToSlash(relPath)

		// Never export our internal state file.
		if relPath == ".export-state.json" {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Always include user-packages.json even in incremental exports,
		// because its content changes whenever packages are pinned/unpinned.
		if incremental && state[relPath] && relPath != "user-packages.json" {
			return nil
		}

		header, hErr := tar.FileInfoHeader(info, "")
		if hErr != nil {
			return hErr
		}
		header.Name = "cache/" + relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		f, fErr := os.Open(path)
		if fErr != nil {
			return fErr
		}
		defer f.Close()
		_, copyErr := io.Copy(tw, f)
		if copyErr == nil {
			newFiles[relPath] = true
		}
		return copyErr
	})
	if err != nil {
		log.Printf("error: export cache archive: %v", err)
		return err
	}

	// Update state on success
	if !incremental {
		state = newFiles
	} else {
		for k := range newFiles {
			state[k] = true
		}
	}
	stateBytes, _ := json.MarshalIndent(struct {
		Files map[string]bool `json:"files"`
	}{Files: state}, "", "  ")
	_ = os.WriteFile(statePath, stateBytes, 0644)

	return nil
}

func (r *cacheRepository) Import(read io.Reader) (int, error) {
	gr, err := gzip.NewReader(read)
	if err != nil {
		return 0, fmt.Errorf("invalid gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	extractedFiles := 0

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("tar read error: %w", err)
		}

		// The export puts everything under "cache/", strip that prefix
		// so we extract directly into r.cacheDir.
		name := filepath.FromSlash(hdr.Name)
		name = strings.TrimPrefix(name, "cache")
		name = strings.TrimPrefix(name, string(filepath.Separator))
		if name == "" || name == "." {
			continue
		}

		target := filepath.Join(r.cacheDir, name)

		// Path traversal protection.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(r.cacheDir)+string(filepath.Separator)) {
			log.Printf("warn: import-cache skip path traversal: %s", hdr.Name)
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return 0, fmt.Errorf("mkdir: %w", err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return 0, fmt.Errorf("mkdir: %w", err)
			}
			f, fErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777))
			if fErr != nil {
				return 0, fmt.Errorf("create file: %w", fErr)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return 0, fmt.Errorf("write file: %w", err)
			}
			f.Close()
			extractedFiles++
		}
	}

	log.Printf("import-cache: extracted %d files", extractedFiles)

	return extractedFiles, nil
}
