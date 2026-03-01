package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// pinnedFilePath returns the path to the pinned packages JSON file.
func (s *server) pinnedFilePath() string {
	return filepath.Join(s.cacheDir, "user-packages.json")
}

// loadPinnedPackages reads pinned packages from disk into memory.
func (s *server) loadPinnedPackages() error {
	data, err := os.ReadFile(s.pinnedFilePath())
	if os.IsNotExist(err) {
		s.pinnedMu.Lock()
		s.pinnedPkgs = make(map[string]pinnedEntry)
		s.pinnedMu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	var entries []pinnedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	m := make(map[string]pinnedEntry, len(entries))
	for _, e := range entries {
		m[e.Module+"@"+e.Version] = e
	}
	s.pinnedMu.Lock()
	s.pinnedPkgs = m
	s.pinnedMu.Unlock()
	return nil
}

// savePinnedPackagesLocked writes pinned packages to disk. Must be called with pinnedMu write-locked.
func (s *server) savePinnedPackagesLocked() error {
	entries := make([]pinnedEntry, 0, len(s.pinnedPkgs))
	for _, e := range s.pinnedPkgs {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		ki := entries[i].Module + "@" + entries[i].Version
		kj := entries[j].Module + "@" + entries[j].Version
		return ki < kj
	})
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.pinnedFilePath(), data, 0o644)
}

// pinPackage records a user-requested package. If the version is empty or
// "latest", it is stored as-is and updated on first resolution.
func (s *server) pinPackage(module, version string) error {
	key := module + "@" + valueOr(version, "latest")
	s.pinnedMu.Lock()
	defer s.pinnedMu.Unlock()
	if s.pinnedPkgs == nil {
		s.pinnedPkgs = make(map[string]pinnedEntry)
	}
	if _, ok := s.pinnedPkgs[key]; ok {
		return nil // already recorded
	}
	s.pinnedPkgs[key] = pinnedEntry{
		Module:   module,
		Version:  valueOr(version, "latest"),
		PinnedAt: time.Now().Format(time.RFC3339),
	}
	return s.savePinnedPackagesLocked()
}

// unpinPackage removes a package from the pinned list.
func (s *server) unpinPackage(module, version string) error {
	key := module + "@" + version
	s.pinnedMu.Lock()
	defer s.pinnedMu.Unlock()
	if _, ok := s.pinnedPkgs[key]; !ok {
		return nil
	}
	delete(s.pinnedPkgs, key)
	return s.savePinnedPackagesLocked()
}

// isPinned reports whether a module@version is in the pinned set.
// It also matches entries recorded as "latest" (before the real version was resolved).
func (s *server) isPinned(module, version string) bool {
	s.pinnedMu.RLock()
	defer s.pinnedMu.RUnlock()
	if _, ok := s.pinnedPkgs[module+"@"+version]; ok {
		return true
	}
	// Fallback: the user may have requested this module without a version,
	// so it was recorded as "latest" before the real version was resolved.
	_, ok := s.pinnedPkgs[module+"@latest"]
	return ok
}

// resolvePinnedLatest updates the "latest" pinned entry for module to the
// resolved concrete version. Safe to call from goroutines.
func (s *server) resolvePinnedLatest(module, resolvedVersion string) {
	latestKey := module + "@latest"
	s.pinnedMu.Lock()
	defer s.pinnedMu.Unlock()
	entry, ok := s.pinnedPkgs[latestKey]
	if !ok {
		return
	}
	// Replace placeholder "latest" with the real version.
	delete(s.pinnedPkgs, latestKey)
	newKey := module + "@" + resolvedVersion
	if _, exists := s.pinnedPkgs[newKey]; !exists {
		entry.Version = resolvedVersion
		s.pinnedPkgs[newKey] = entry
	}
	_ = s.savePinnedPackagesLocked()
}

// handlePinned handles GET/DELETE /api/pinned
func (s *server) handlePinned(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.pinnedMu.RLock()
		entries := make([]pinnedEntry, 0, len(s.pinnedPkgs))
		for _, e := range s.pinnedPkgs {
			entries = append(entries, e)
		}
		s.pinnedMu.RUnlock()
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Module+"@"+entries[i].Version < entries[j].Module+"@"+entries[j].Version
		})
		writeJSON(w, http.StatusOK, entries)
	case http.MethodDelete:
		var req struct {
			Module  string `json:"module"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		if req.Module == "" || req.Version == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "module and version required"})
			return
		}
		if err := s.unpinPackage(req.Module, req.Version); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *server) appendProxyLog(line string) {
	s.proxyLogsMu.Lock()
	s.proxyLogs = append(s.proxyLogs, line)
	if len(s.proxyLogs) > 500 {
		s.proxyLogs = s.proxyLogs[len(s.proxyLogs)-500:]
	}
	s.proxyLogsMu.Unlock()
}

func (s *server) snapshotProxyLogs(limit int) []string {
	s.proxyLogsMu.Lock()
	defer s.proxyLogsMu.Unlock()
	if limit <= 0 || limit > len(s.proxyLogs) {
		limit = len(s.proxyLogs)
	}
	start := len(s.proxyLogs) - limit
	return append([]string(nil), s.proxyLogs[start:]...)
}
