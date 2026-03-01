package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *server) listCachedModules(query string) ([]cachedModule, error) {
	base := s.proxyBaseDir()
	out := make([]cachedModule, 0, 128)
	query = strings.ToLower(strings.TrimSpace(query))

	exportStatePath := filepath.Join(s.cacheDir, ".export-state.json")
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
		modPath, err := unescapeModulePath(escapedMod)
		if err != nil {
			modPath = escapedMod
		}

		escapedVer := strings.TrimSuffix(parts[len(parts)-1], ".info")
		ver, err := unescapeModuleVersion(escapedVer)
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

		row := cachedModule{
			Module:   modPath,
			Version:  ver,
			Pinned:   s.isPinned(modPath, ver),
			Exported: exportedPaths[relFromCache],
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
		return nil, err
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

func (s *server) handleExportCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	incremental := r.URL.Query().Get("incremental") == "true"
	statePath := filepath.Join(s.cacheDir, ".export-state.json")

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
		_ = filepath.Walk(s.cacheDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, relErr := filepath.Rel(s.cacheDir, path)
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
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	filename := fmt.Sprintf("go-offline-cache-%s.tar.gz", time.Now().Format("20060102-150405"))
	if incremental {
		filename = fmt.Sprintf("go-offline-cache-inc-%s.tar.gz", time.Now().Format("20060102-150405"))
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	baseDir := s.cacheDir
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
	} else {
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
	}
}

func (s *server) handleImportCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Limit upload size to 4 GB.
	r.Body = http.MaxBytesReader(w, r.Body, 4<<30)

	file, _, err := r.FormFile("archive")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid archive file: " + err.Error()})
		return
	}
	defer file.Close()

	gr, err := gzip.NewReader(file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid gzip: " + err.Error()})
		return
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
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tar read error: " + err.Error()})
			return
		}

		// The export puts everything under "cache/", strip that prefix
		// so we extract directly into s.cacheDir.
		name := filepath.FromSlash(hdr.Name)
		name = strings.TrimPrefix(name, "cache")
		name = strings.TrimPrefix(name, string(filepath.Separator))
		if name == "" || name == "." {
			continue
		}

		target := filepath.Join(s.cacheDir, name)

		// Path traversal protection.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(s.cacheDir)+string(filepath.Separator)) {
			log.Printf("warn: import-cache skip path traversal: %s", hdr.Name)
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir: " + err.Error()})
				return
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir: " + err.Error()})
				return
			}
			f, fErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777))
			if fErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create file: " + fErr.Error()})
				return
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write file: " + err.Error()})
				return
			}
			f.Close()
			extractedFiles++
		}
	}

	log.Printf("import-cache: extracted %d files", extractedFiles)

	// Reload pinned packages from the (possibly updated) user-packages.json.
	if err := s.loadPinnedPackages(); err != nil {
		log.Printf("warn: reload pinned packages after import: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"extracted_files": extractedFiles,
		"message":         fmt.Sprintf("Импортировано %d файлов", extractedFiles),
	})
}
