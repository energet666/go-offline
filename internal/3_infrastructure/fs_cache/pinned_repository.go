package fs_cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go-offline/internal/1_domain/cache"
)

type pinnedRepository struct {
	cacheDir string
	mu       sync.RWMutex
	pkgs     map[string]cache.PinnedEntry // key: "module@version"
}

// NewPinnedRepository creates a new file-system backed pinned repository and loads data from disk.
func NewPinnedRepository(cacheDir string) (cache.PinnedRepository, error) {
	repo := &pinnedRepository{
		cacheDir: cacheDir,
		pkgs:     make(map[string]cache.PinnedEntry),
	}
	if err := repo.load(); err != nil {
		return nil, err
	}
	return repo, nil
}

func (r *pinnedRepository) filePath() string {
	return filepath.Join(r.cacheDir, "user-packages.json")
}

func (r *pinnedRepository) load() error {
	data, err := os.ReadFile(r.filePath())
	if os.IsNotExist(err) {
		r.mu.Lock()
		r.pkgs = make(map[string]cache.PinnedEntry)
		r.mu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	var entries []cache.PinnedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	m := make(map[string]cache.PinnedEntry, len(entries))
	for _, e := range entries {
		m[e.Module+"@"+e.Version] = e
	}
	r.mu.Lock()
	r.pkgs = m
	r.mu.Unlock()
	return nil
}

// saveLocked writes pinned packages to disk. Must be called with mu write-locked.
func (r *pinnedRepository) saveLocked() error {
	entries := make([]cache.PinnedEntry, 0, len(r.pkgs))
	for _, e := range r.pkgs {
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
	return os.WriteFile(r.filePath(), data, 0o644)
}

func valueOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func (r *pinnedRepository) Pin(module, version string) error {
	key := module + "@" + valueOr(version, "latest")
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pkgs == nil {
		r.pkgs = make(map[string]cache.PinnedEntry)
	}
	if _, ok := r.pkgs[key]; ok {
		return nil // already recorded
	}
	r.pkgs[key] = cache.PinnedEntry{
		Module:   module,
		Version:  valueOr(version, "latest"),
		PinnedAt: time.Now().Format(time.RFC3339),
	}
	return r.saveLocked()
}

func (r *pinnedRepository) Unpin(module, version string) error {
	key := module + "@" + version
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pkgs[key]; !ok {
		return nil
	}
	delete(r.pkgs, key)
	return r.saveLocked()
}

func (r *pinnedRepository) IsPinned(module, version string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.pkgs[module+"@"+version]; ok {
		return true
	}
	// Fallback: the user may have requested this module without a version,
	// so it was recorded as "latest" before the real version was resolved.
	_, ok := r.pkgs[module+"@latest"]
	return ok
}

func (r *pinnedRepository) ResolvePinnedLatest(module, resolvedVersion string) {
	latestKey := module + "@latest"
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.pkgs[latestKey]
	if !ok {
		return
	}
	// Replace placeholder "latest" with the real version.
	delete(r.pkgs, latestKey)
	newKey := module + "@" + resolvedVersion
	if _, exists := r.pkgs[newKey]; !exists {
		entry.Version = resolvedVersion
		r.pkgs[newKey] = entry
	}
	_ = r.saveLocked()
}

func (r *pinnedRepository) List() []cache.PinnedEntry {
	r.mu.RLock()
	entries := make([]cache.PinnedEntry, 0, len(r.pkgs))
	for _, e := range r.pkgs {
		entries = append(entries, e)
	}
	r.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Module+"@"+entries[i].Version < entries[j].Module+"@"+entries[j].Version
	})
	return entries
}

func (r *pinnedRepository) Reload() error {
	return r.load()
}
