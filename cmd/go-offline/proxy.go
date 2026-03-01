package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *server) resolveVersion(ctx context.Context, modPath, requestedVersion string) (string, error) {
	if requestedVersion != "" && requestedVersion != "latest" {
		return requestedVersion, nil
	}

	escapedPath, err := escapeModulePath(modPath)
	if err != nil {
		return "", err
	}
	u := s.upstream + "/" + escapedPath + "/@latest"
	body, err := s.fetchRemote(ctx, u, nil)
	if err != nil {
		return "", err
	}
	var latest struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(body, &latest); err != nil {
		return "", err
	}
	if latest.Version == "" {
		return "", errors.New("empty latest version")
	}
	return latest.Version, nil
}

func (s *server) downloadModule(ctx context.Context, modPath, version string, logf func(string, ...any)) (bool, []byte, int64, error) {
	escapedPath, err := escapeModulePath(modPath)
	if err != nil {
		return false, nil, 0, err
	}
	escapedVersion, err := escapeModuleVersion(version)
	if err != nil {
		return false, nil, 0, err
	}

	dir := filepath.Join(s.workDir, "proxy", filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, nil, 0, err
	}
	modFile := filepath.Join(dir, escapedVersion+".mod")
	if _, err := os.Stat(modFile); err == nil {
		content, readErr := os.ReadFile(modFile)
		return false, content, 0, readErr
	}

	infoURL := s.upstream + "/" + escapedPath + "/@v/" + escapedVersion + ".info"
	modURL := s.upstream + "/" + escapedPath + "/@v/" + escapedVersion + ".mod"
	zipURL := s.upstream + "/" + escapedPath + "/@v/" + escapedVersion + ".zip"

	infoBody, err := s.fetchRemote(ctx, infoURL, logf)
	if err != nil {
		return false, nil, 0, err
	}
	logf("fetched info %s@%s", modPath, version)
	modBody, err := s.fetchRemote(ctx, modURL, logf)
	if err != nil {
		return false, nil, 0, err
	}
	logf("fetched mod %s@%s", modPath, version)
	zipBody, err := s.fetchRemote(ctx, zipURL, logf)
	if err != nil {
		return false, nil, 0, err
	}
	logf("fetched zip %s@%s", modPath, version)

	if err := os.WriteFile(filepath.Join(dir, escapedVersion+".info"), infoBody, 0o644); err != nil {
		return false, nil, 0, err
	}
	if err := os.WriteFile(modFile, modBody, 0o644); err != nil {
		return false, nil, 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, escapedVersion+".zip"), zipBody, 0o644); err != nil {
		return false, nil, 0, err
	}
	if err := s.updateVersionList(dir, version); err != nil {
		return false, nil, 0, err
	}
	total := int64(len(infoBody) + len(modBody) + len(zipBody))
	return true, modBody, total, nil
}

func (s *server) fetchRemote(ctx context.Context, rawURL string, logf func(string, ...any)) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= s.fetchRetries; attempt++ {
		if attempt > 0 && logf != nil {
			logf("retry %d/%d for %s", attempt, s.fetchRetries, rawURL)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < s.fetchRetries && isRetryableError(err) {
				time.Sleep(s.retryBackoff * time.Duration(attempt+1))
				continue
			}
			return nil, err
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < s.fetchRetries && isRetryableError(readErr) {
				time.Sleep(s.retryBackoff * time.Duration(attempt+1))
				continue
			}
			return nil, readErr
		}
		if resp.StatusCode != http.StatusOK {
			msg := fmt.Errorf("upstream %s: %s (%s)", rawURL, resp.Status, strings.TrimSpace(string(limitBytes(body, 2048))))
			lastErr = msg
			if attempt < s.fetchRetries && isRetryableStatus(resp.StatusCode) {
				time.Sleep(s.retryBackoff * time.Duration(attempt+1))
				continue
			}
			return nil, msg
		}
		return body, nil
	}
	if lastErr == nil {
		lastErr = errors.New("upstream request failed")
	}
	return nil, lastErr
}

// resolveModulePath resolves a package path to the root module path by probing
// the upstream GOPROXY. It tries successively shorter path prefixes (from
// longest to shortest) until it finds one that the proxy recognises as a valid
// module. If the path itself is already a valid module path it is returned
// unchanged.
func (s *server) resolveModulePath(ctx context.Context, pkgPath, version string) (string, error) {
	parts := strings.Split(pkgPath, "/")
	// Try from longest to shortest prefix.
	// We start at the full path and walk up to the root.
	for i := len(parts); i >= 1; i-- {
		candidate := strings.Join(parts[:i], "/")
		escapedPath, err := escapeModulePath(candidate)
		if err != nil {
			continue
		}
		var probeURL string
		if version != "" && version != "latest" {
			// Probe a specific version via /@v/<version>.info
			escapedVer, err := escapeModuleVersion(version)
			if err != nil {
				continue
			}
			probeURL = s.upstream + "/" + escapedPath + "/@v/" + escapedVer + ".info"
		} else {
			// Probe the latest endpoint
			probeURL = s.upstream + "/" + escapedPath + "/@latest"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err != nil {
			continue
		}
		resp, err := s.httpClient.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return candidate, nil
		}
	}
	// Fallback: return the original path; go mod download will produce a clear error.
	return pkgPath, nil
}

// resolveVersionFromCache finds the latest cached version of modPath by scanning
// the proxy cache directory for .info files.
func (s *server) resolveVersionFromCache(modPath string) (string, error) {
	escapedPath, err := escapeModulePath(modPath)
	if err != nil {
		return "", err
	}
	versionDir := filepath.Join(s.proxyBaseDir(), filepath.FromSlash(escapedPath), "@v")
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return "", err
	}
	var best string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".info") {
			continue
		}
		escapedVer := strings.TrimSuffix(ent.Name(), ".info")
		ver, err := unescapeModuleVersion(escapedVer)
		if err != nil {
			ver = escapedVer
		}
		if best == "" || compareModuleVersions(ver, best) > 0 {
			best = ver
		}
	}
	if best == "" {
		return "", errors.New("no cached versions found")
	}
	return best, nil
}

func (s *server) proxyBaseDir() string {
	newBase := filepath.Join(s.cacheDir, "gomodcache", "cache", "download")
	if st, err := os.Stat(newBase); err == nil && st.IsDir() {
		return newBase
	}
	return filepath.Join(s.workDir, "proxy")
}

func (s *server) updateVersionList(versionDir, version string) error {
	listFile := filepath.Join(versionDir, "list")
	set := map[string]struct{}{version: {}}
	if data, err := os.ReadFile(listFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				set[line] = struct{}{}
			}
		}
	}
	var versions []string
	for v := range set {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	out := strings.Join(versions, "\n")
	if out != "" {
		out += "\n"
	}
	return os.WriteFile(listFile, []byte(out), 0o644)
}

func (s *server) serveProxyFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rel := strings.TrimPrefix(r.URL.Path, "/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	rel, err := url.PathUnescape(rel)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if strings.HasSuffix(rel, "/@latest") {
		if s.serveLatestFromCache(w, r, strings.TrimSuffix(rel, "/@latest")) {
			return
		}
		http.NotFound(w, r)
		return
	}
	target := filepath.Join(s.proxyBaseDir(), filepath.FromSlash(rel))
	if st, err := os.Stat(target); err == nil && !st.IsDir() {
		http.ServeFile(w, r, target)
		return
	}
	legacyTarget := filepath.Join(s.workDir, "proxy", filepath.FromSlash(rel))
	if st, err := os.Stat(legacyTarget); err == nil && !st.IsDir() {
		http.ServeFile(w, r, legacyTarget)
		return
	}

	http.NotFound(w, r)
}

func (s *server) serveLatestFromCache(w http.ResponseWriter, r *http.Request, escapedModulePath string) bool {
	type latestCandidate struct {
		version string
		body    []byte
	}

	baseDirs := []string{s.proxyBaseDir(), filepath.Join(s.workDir, "proxy")}
	var best *latestCandidate

	for _, base := range baseDirs {
		versionDir := filepath.Join(base, filepath.FromSlash(escapedModulePath), "@v")
		entries, err := os.ReadDir(versionDir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".info") {
				continue
			}

			escapedVersion := strings.TrimSuffix(ent.Name(), ".info")
			version, err := unescapeModuleVersion(escapedVersion)
			if err != nil {
				version = escapedVersion
			}

			infoPath := filepath.Join(versionDir, ent.Name())
			body, err := os.ReadFile(infoPath)
			if err != nil {
				continue
			}

			if best == nil || compareModuleVersions(version, best.version) > 0 {
				best = &latestCandidate{
					version: version,
					body:    body,
				}
			}
		}
	}

	if best == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	http.ServeContent(w, r, "@latest", time.Time{}, strings.NewReader(string(best.body)))
	return true
}
