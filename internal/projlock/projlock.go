// Copyright 2026 The Graft Authors

// Package projlock serializes mutating graft commands on the same project
// (spec §5.7): an exclusive advisory file lock in the global cache, keyed by
// the project root. A second graft process blocks until the first finishes,
// printing a hint when the wait exceeds a second — the cargo/uv behavior.
package projlock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/min0625/graft/internal/cachedir"
)

// waitHint is printed to warn once the lock has been contended for longer
// than warnAfter.
const waitHint = "waiting for another graft process to finish…"

const (
	warnAfter    = time.Second
	pollInterval = 50 * time.Millisecond
)

// Acquire takes the exclusive per-project advisory lock for the project
// rooted at root, blocking while another graft process holds it and printing
// waitHint to warn after one second of waiting. The returned release func
// must be called once the project's manifest, lockfile, and vendor directory
// are consistent again.
//
// The lock file lives in the global cache — never in the repository — at
// locks/projects/<sha256 of the resolved root path> (spec §5.7).
func Acquire(ctx context.Context, root string, warn io.Writer) (release func(), err error) {
	path, err := lockPath(root)
	if err != nil {
		return nil, err
	}

	//nolint:gosec // The cache is world-readable by design, like the vendor tree.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create project lock directory: %w", err)
	}

	fl := flock.New(path)
	start := time.Now()
	warned := false

	for {
		locked, err := fl.TryLock()
		if err != nil {
			return nil, fmt.Errorf("acquire project lock: %w", err)
		}

		if locked {
			return func() { fl.Unlock() }, nil //nolint:errcheck,gosec // Releasing an advisory lock is best effort.
		}

		if !warned && time.Since(start) >= warnAfter {
			fmt.Fprintln(warn, waitHint) //nolint:errcheck // Best-effort hint, like CLI output.

			warned = true
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("acquire project lock: %w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// lockPath maps a project root to its lock file in the global cache. The key
// is the hash of the symlink-resolved absolute root, so every spelling of
// the same project directory contends on the same lock.
func lockPath(root string) (string, error) {
	cache, err := cachedir.Dir()
	if err != nil {
		return "", err
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}

	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	sum := sha256.Sum256([]byte(abs))

	return filepath.Join(cache, "locks", "projects", hex.EncodeToString(sum[:])), nil
}
