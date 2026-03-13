package httphandlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// resolveModulePath resolves a package path to the root module path by probing
// the upstream GOPROXY. It tries successively shorter path prefixes (from
// longest to shortest) until it finds one that the proxy recognises as a valid
// module. If the path itself is already a valid module path it is returned
// unchanged.
func (s *Server) resolveModulePath(ctx context.Context, pkgPath, version string) (string, error) {
	parts := strings.Split(pkgPath, "/")
	for i := len(parts); i >= 1; i-- {
		candidate := strings.Join(parts[:i], "/")
		escapedPath, err := module.EscapePath(candidate)
		if err != nil {
			continue
		}
		var probeURL string
		if version != "" && version != "latest" {
			escapedVer, err := module.EscapeVersion(version)
			if err != nil {
				continue
			}
			probeURL = s.upstream + "/" + escapedPath + "/@v/" + escapedVer + ".info"
		} else {
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
	return pkgPath, nil
}

// resolveVersionFromCache finds the latest cached version of modPath by scanning
// the proxy cache directory for .info files.
func (s *Server) resolveVersionFromCache(modPath string) (string, error) {
	escapedPath, err := module.EscapePath(modPath)
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
		ver, err := module.UnescapeVersion(escapedVer)
		if err != nil {
			ver = escapedVer
		}
		if best == "" || semver.Compare(ver, best) > 0 {
			best = ver
		}
	}
	if best == "" {
		return "", errors.New("no cached versions found")
	}
	return best, nil
}

func (s *Server) ServeProxyFile(w http.ResponseWriter, r *http.Request) {
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

	http.NotFound(w, r)
}

func (s *Server) serveLatestFromCache(w http.ResponseWriter, r *http.Request, escapedModulePath string) bool {
	type latestCandidate struct {
		version string
		body    []byte
	}

	base := s.proxyBaseDir()
	var best *latestCandidate

	versionDir := filepath.Join(base, filepath.FromSlash(escapedModulePath), "@v")
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return false
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".info") {
			continue
		}

		escapedVersion := strings.TrimSuffix(ent.Name(), ".info")
		version, err := module.UnescapeVersion(escapedVersion)
		if err != nil {
			version = escapedVersion
		}

		infoPath := filepath.Join(versionDir, ent.Name())
		body, err := os.ReadFile(infoPath)
		if err != nil {
			continue
		}

		if best == nil || semver.Compare(version, best.version) > 0 {
			best = &latestCandidate{
				version: version,
				body:    body,
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
