// Copyright 2026 The Graft Authors

// Package cachedir resolves graft's per-user global cache directory
// (spec §5.6): the OS user cache convention, overridable with
// GRAFT_CACHE_DIR. The cache is purely a performance layer — deleting it is
// always safe.
package cachedir

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvOverride is the environment variable that overrides the cache location.
const EnvOverride = "GRAFT_CACHE_DIR"

// Subdirectory names under the cache root (spec §5.6). locks/ is created on
// demand by the projlock package; the other four are created by the helpers
// below.
const (
	ReposSubdir = "repos" // bare repositories, incrementally fetched, shared
	StoreSubdir = "store" // immutable content trees keyed by lockfile hash
	LinksSubdir = "links" // registry of link-mode dests, for `cache prune`
	TmpSubdir   = "tmp"   // checkout staging, on the same filesystem as store
	LocksSubdir = "locks" // per-project advisory locks, managed by projlock
)

// tagFile / tagSignature implement the Freedesktop Cache Directory Tagging
// Spec (https://bford.info/cachedir/): a marker that tells backup tools and
// clean scripts that the directory is a cache (and lets us verify we own it
// before deleting anything).
const (
	tagFile      = "CACHEDIR.TAG"
	tagSignature = "Signature: 8a477f597d28d172789f06886806bc55"
	tagContent   = tagSignature + "\n# This file marks the directory as a cache managed by graft.\n# See https://bford.info/cachedir/\n"
)

// HasTag reports whether root contains a valid CACHEDIR.TAG, i.e. that it was
// created by graft. cache clean refuses to proceed without this marker so that
// GRAFT_CACHE_DIR=/some/wrong/path never rm -rfs user data.
func HasTag(root string) bool {
	//nolint:gosec // tagFile is a package constant; root comes from Dir(), not user input.
	data, err := os.ReadFile(filepath.Join(root, tagFile))
	return err == nil && strings.HasPrefix(string(data), tagSignature)
}

// writeTag writes CACHEDIR.TAG to root if not already present.
func writeTag(root string) error {
	//nolint:gosec // tagFile is a package constant; root comes from Dir(), not user input.
	f, err := os.OpenFile(filepath.Join(root, tagFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("write cache tag: %w", err)
	}

	defer f.Close() //nolint:errcheck

	if _, err := f.WriteString(tagContent); err != nil {
		return fmt.Errorf("write cache tag: %w", err)
	}

	return nil
}

// Dir returns graft's cache directory without creating it: $GRAFT_CACHE_DIR
// when set, otherwise the per-OS user cache location of spec §5.6 —
// ~/.cache/graft on Linux, ~/Library/Caches/graft on macOS, and
// %LocalAppData%\graft\cache on Windows.
func Dir() (string, error) {
	if dir := os.Getenv(EnvOverride); dir != "" {
		return filepath.Abs(dir)
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

// DirSize returns the total logical size of every regular file under root, the
// number `cache prune`/`cache clean` report as reclaimed. A missing root is 0.
// ponytail: logical file size, not on-disk blocks — reflinked entries share
// blocks, so this slightly overstates disk actually freed; honest enough for a
// human-facing "reclaimed X" line.
func DirSize(root string) (int64, error) {
	var total int64

	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}

			return err
		}

		if !d.Type().IsRegular() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}

			return err
		}

		total += info.Size()

		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure %s: %w", root, err)
	}

	return total, nil
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

	if err := writeTag(base); err != nil {
		return "", err
	}

	return path, nil
}
