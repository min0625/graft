// Copyright 2026 The Graft Authors

// Package cachedir resolves graft's per-user global cache directory
// (spec §5.6): the OS user cache convention, overridable with
// GRAFT_CACHE_DIR. The cache is purely a performance layer — deleting it is
// always safe.
package cachedir

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// EnvOverride is the environment variable that overrides the cache location.
const EnvOverride = "GRAFT_CACHE_DIR"

// Subdirectory names under the cache root (spec §5.6). locks/ is created on
// demand by the projlock package; the other four are created by the helpers
// below.
const (
	ReposSubdir = "repos" // bare repositories, incrementally fetched, shared
	StoreSubdir = "store" // immutable content trees keyed by lockfile hash
	LinksSubdir = "links" // registry of link-mode dests, for `cache clean`
	TmpSubdir   = "tmp"   // checkout staging, on the same filesystem as store
)

// Dir returns graft's cache directory without creating it: $GRAFT_CACHE_DIR
// when set, otherwise the per-OS user cache location of spec §5.6 —
// ~/.cache/graft on Linux, ~/Library/Caches/graft on macOS, and
// %LocalAppData%\graft\cache on Windows.
func Dir() (string, error) {
	if dir := os.Getenv(EnvOverride); dir != "" {
		return dir, nil
	}

	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}

	if runtime.GOOS == "windows" {
		// os.UserCacheDir is %LocalAppData% itself, which apps share with
		// their non-cache data — keep the cache in its own subdirectory.
		return filepath.Join(base, "graft", "cache"), nil
	}

	return filepath.Join(base, "graft"), nil
}

// Repos returns <cache>/repos, creating it (and the cache root) if needed.
func Repos() (string, error) { return ensureSubdir(ReposSubdir) }

// Store returns <cache>/store, creating it (and the cache root) if needed.
func Store() (string, error) { return ensureSubdir(StoreSubdir) }

// Links returns <cache>/links, creating it (and the cache root) if needed.
func Links() (string, error) { return ensureSubdir(LinksSubdir) }

// Tmp returns <cache>/tmp, creating it (and the cache root) if needed.
func Tmp() (string, error) { return ensureSubdir(TmpSubdir) }

// ensureSubdir resolves <cache>/name and lazily creates it. The cache is a
// pure performance layer (spec §5.6), so callers create only what they touch.
func ensureSubdir(name string) (string, error) {
	base, err := Dir()
	if err != nil {
		return "", err
	}

	path := filepath.Join(base, name)

	//nolint:gosec // The cache is world-readable by design, like the vendor tree.
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("create cache %s directory: %w", name, err)
	}

	return path, nil
}
