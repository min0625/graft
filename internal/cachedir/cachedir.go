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
