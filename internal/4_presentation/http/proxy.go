package httphandlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// splitModuleQuery accepts a "path@version" query in the module field, which is
// how a module is spelled everywhere else (go get, the copy button in the UI),
// and reports the path and version separately. An empty version field takes the
// version from the query; a filled one has to agree with it, because silently
// picking either would download something other than what was asked for.
func splitModuleQuery(modQuery, version string) (string, string, error) {
	path, queryVersion, found := strings.Cut(modQuery, "@")
	path = strings.TrimSpace(path)
	queryVersion = strings.TrimSpace(queryVersion)
	if !found {
		return path, version, nil
	}
	if path == "" {
		return "", "", fmt.Errorf("не указан путь модуля перед @ в %q", modQuery)
	}
	if queryVersion == "" {
		return "", "", fmt.Errorf("не указана версия после @ в %q", modQuery)
	}
	if version != "" && version != queryVersion {
		return "", "", fmt.Errorf("версия указана дважды и по-разному: %q и %q", queryVersion, version)
	}
	return path, queryVersion, nil
}

// resolveModulePath resolves a package path to the root module path by probing
// the upstream GOPROXY. It tries successively shorter path prefixes (from
// longest to shortest) until it finds one that the proxy recognises as a valid
// module. If the path itself is already a valid module path it is returned
// unchanged.
//
// The path must be a syntactically valid import path: a malformed one has no
// valid prefix to fall back to either, and shortening it until something
// happens to resolve would quietly download a different module (e.g.
// "fyne.io/fyne/v2@latest" pasted as a path resolving to fyne.io/fyne v1).
func (s *Server) resolveModulePath(ctx context.Context, pkgPath, version string) (string, error) {
	if err := module.CheckImportPath(pkgPath); err != nil {
		return "", err
	}
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

// cachedVersions returns every version of a module present in the proxy cache,
// sorted ascending by semver. escapedModulePath is the proxy-escaped path, as
// it appears in the request and on disk.
func (s *Server) cachedVersions(escapedModulePath string) []string {
	versionDir := filepath.Join(s.proxyBaseDir(), filepath.FromSlash(escapedModulePath), "@v")
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return nil
	}
	versions := make([]string, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".info") {
			continue
		}
		escapedVer := strings.TrimSuffix(ent.Name(), ".info")
		ver, err := module.UnescapeVersion(escapedVer)
		if err != nil {
			ver = escapedVer
		}
		versions = append(versions, ver)
	}
	sort.Slice(versions, func(i, j int) bool {
		return semver.Compare(versions[i], versions[j]) < 0
	})
	return versions
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
	// Answer /@v/list from the cache instead of serving the "list" file the go
	// tool sometimes leaves there: that file enumerates every version upstream
	// has, so an offline client would resolve @latest to something we never
	// downloaded and then 404 on the .info. Without an answer here go cannot
	// resolve @latest at all — it does not fall back to the /@latest endpoint.
	if strings.HasSuffix(rel, "/@v/list") {
		if s.serveVersionListFromCache(w, r, strings.TrimSuffix(rel, "/@v/list")) {
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

// serveVersionListFromCache answers the /@v/list endpoint with the versions of
// the module that are actually in the cache.
func (s *Server) serveVersionListFromCache(w http.ResponseWriter, r *http.Request, escapedModulePath string) bool {
	versions := s.cachedVersions(escapedModulePath)
	if len(versions) == 0 {
		return false
	}
	var list strings.Builder
	for _, version := range versions {
		// Upstream proxies leave pseudo-versions out of the list, and go only
		// ever reaches them through an exact request anyway.
		if module.IsPseudoVersion(version) {
			continue
		}
		list.WriteString(version)
		list.WriteString("\n")
	}
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	http.ServeContent(w, r, "list", time.Time{}, strings.NewReader(list.String()))
	return true
}

func (s *Server) serveLatestFromCache(w http.ResponseWriter, r *http.Request, escapedModulePath string) bool {
	versions := s.cachedVersions(escapedModulePath)
	if len(versions) == 0 {
		return false
	}
	// Same preference go applies to the version list: the newest release, and
	// only if there is none, the newest pre-release or pseudo-version.
	best := versions[len(versions)-1]
	for i := len(versions) - 1; i >= 0; i-- {
		if semver.Prerelease(versions[i]) == "" {
			best = versions[i]
			break
		}
	}

	escapedVersion, err := module.EscapeVersion(best)
	if err != nil {
		return false
	}
	body, err := os.ReadFile(filepath.Join(s.proxyBaseDir(), filepath.FromSlash(escapedModulePath), "@v", escapedVersion+".info"))
	if err != nil {
		return false
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	http.ServeContent(w, r, "@latest", time.Time{}, strings.NewReader(string(body)))
	return true
}
