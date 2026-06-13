// Copyright 2026 The Graft Authors

// Package vendor reconciles the on-disk vendor state with the lockfile
// (spec §5.3): missing deps are installed, extras removed, and mismatched
// trees replaced, with every install staged in <vendor>/.graft-tmp and moved
// into place by an atomic same-filesystem rename.
package vendor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gitrun"
	"github.com/min0625/graft/internal/hasher"
	"github.com/min0625/graft/internal/lockfile"
)

// StagingDirName is the staging directory under the vendor root. It is never
// treated as an extra dep, and stale leftovers in it are deleted at the
// start of every reconcile.
const StagingDirName = ".graft-tmp"

// FetchFunc materializes the tree of dep into dst, which does not exist yet
// but whose parent directory does.
type FetchFunc func(ctx context.Context, dep lockfile.LockedDep, dst string) error

// Result reports what a reconcile changed.
type Result struct {
	// Installed lists the deps that were fetched and (re)installed, in
	// lockfile order. Deps whose dest already matched are not listed.
	Installed []lockfile.LockedDep
	// Removed lists extra paths deleted from the vendor directory, relative
	// to the project root and slash-separated.
	Removed []string
}

// Changed reports whether the reconcile modified anything.
func (r *Result) Changed() bool {
	return len(r.Installed) > 0 || len(r.Removed) > 0
}

// Reconcile makes the tree under root match deps exactly. Each dep whose
// dest content hash differs from the locked hash is fetched, verified
// (mismatch is exit 4), and swapped into place; paths under vendorDir owned
// by no locked dest are removed. Custom dests outside vendorDir are
// installed but never garbage-collected.
func Reconcile(
	ctx context.Context,
	root, vendorDir string,
	deps []lockfile.LockedDep,
	fetch FetchFunc,
) (*Result, error) {
	vendorAbs := filepath.Join(root, filepath.FromSlash(vendorDir))
	staging := filepath.Join(vendorAbs, StagingDirName)

	// Clean staging left behind by an interrupted run (spec §5.3).
	if err := gitrun.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("clean stale staging: %w", err)
	}

	if err := os.MkdirAll(staging, 0o755); err != nil { //nolint:gosec // Vendor trees are world-readable by design.
		return nil, fmt.Errorf("create staging dir: %w", err)
	}

	var result Result

	installed, err := reconcileDeps(ctx, root, staging, deps, fetch)
	if err != nil {
		return nil, err
	}

	for i, dep := range deps {
		if installed[i] {
			result.Installed = append(result.Installed, dep)
		}
	}

	removed, err := removeExtras(vendorAbs, vendorDir, deps)
	if err != nil {
		return nil, err
	}

	result.Removed = removed

	if err := gitrun.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("remove staging dir: %w", err)
	}

	// Drop the vendor directory itself when the reconcile left it empty.
	os.Remove(vendorAbs) //nolint:errcheck,gosec // Only succeeds on an empty directory; best effort.

	return &result, nil
}

// reconcileDeps runs reconcileDep for every dep on a worker pool capped at
// min(numDeps, runtime.NumCPU()) (spec §5.4). Errors are collected — not
// fail-fast — and joined in lockfile order, so one run surfaces every
// failure at once.
func reconcileDeps(
	ctx context.Context,
	root, staging string,
	deps []lockfile.LockedDep,
	fetch FetchFunc,
) ([]bool, error) {
	installed := make([]bool, len(deps))
	errs := make([]error, len(deps))
	jobs := make(chan int)

	var wg sync.WaitGroup

	for range min(len(deps), runtime.NumCPU()) {
		wg.Go(func() {
			for i := range jobs {
				installed[i], errs[i] = reconcileDep(ctx, root, staging, deps[i], i, fetch)
			}
		})
	}

	for i := range deps {
		jobs <- i
	}

	close(jobs)
	wg.Wait()

	return installed, errors.Join(errs...)
}

// reconcileDep brings one dep's dest in line with the lockfile and reports
// whether it had to (re)install.
func reconcileDep(
	ctx context.Context,
	root, staging string,
	dep lockfile.LockedDep,
	seq int,
	fetch FetchFunc,
) (bool, error) {
	destAbs := filepath.Join(root, filepath.FromSlash(dep.Dest))

	// `graft apply` verifies content hashes even for an already-present
	// dest (spec §10.2); any divergence — including a hand-edited vendor
	// tree — is repaired by reinstalling from the locked commit.
	if _, err := os.Lstat(destAbs); err == nil {
		if got, err := hasher.HashTree(destAbs); err == nil && got == dep.Hash {
			return false, nil
		}
	}

	fetchDst := filepath.Join(staging, "new-"+strconv.Itoa(seq))

	if err := fetch(ctx, dep, fetchDst); err != nil {
		return false, err
	}

	got, err := hasher.HashTree(fetchDst)
	if err != nil {
		return false, err
	}

	if got != dep.Hash {
		return false, integrityErr(dep, got)
	}

	if err := install(staging, fetchDst, destAbs, seq); err != nil {
		return false, fmt.Errorf("install %q: %w", dep.Name, err)
	}

	return true, nil
}

// integrityErr is the spec §6 exit-4 error: the content fetched for the
// locked commit does not hash to what the lockfile records.
func integrityErr(dep lockfile.LockedDep, got string) error {
	return clierr.New(clierr.CodeIntegrity,
		fmt.Sprintf("content integrity check failed for %q", dep.Name),
		"expected  "+dep.Hash+"\ngot       "+got,
		"the installed content does not match what was locked — usually a\n"+
			"hand-edited vendor directory or a manually altered lockfile\n"+
			fmt.Sprintf("run `graft apply` after restoring the lockfile, or `graft add %s@<ref>`\n", dep.Name)+
			"to deliberately re-pin and re-lock",
	)
}

// install swaps the verified tree at src into destAbs: the old dest (if any)
// is parked in staging first, then src is renamed into place — an atomic
// same-filesystem rename in the common case, with a copy fallback for a
// custom dest on another filesystem.
func install(staging, src, destAbs string, seq int) error {
	//nolint:gosec // Vendor trees are world-readable by design.
	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return err
	}

	if _, err := os.Lstat(destAbs); err == nil {
		old := filepath.Join(staging, "old-"+strconv.Itoa(seq))
		if err := os.Rename(destAbs, old); err != nil {
			// Different filesystem: delete in place instead of parking.
			if err := gitrun.RemoveAll(destAbs); err != nil {
				return err
			}
		}
	}

	if err := os.Rename(src, destAbs); err == nil {
		return nil
	}

	if err := copyTree(src, destAbs); err != nil {
		return err
	}

	return gitrun.RemoveAll(src)
}

// removeExtras deletes everything under the vendor directory that no locked
// dest owns, leaving the staging directory and the locked dests themselves
// alone, and returns the removed paths relative to the project root.
func removeExtras(vendorAbs, vendorRel string, deps []lockfile.LockedDep) ([]string, error) {
	owned := make(map[string]bool, len(deps))
	for _, dep := range deps {
		owned[dep.Dest] = true
	}

	if _, err := os.Stat(vendorAbs); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var removed []string

	var walk func(abs, rel string) error

	walk = func(abs, rel string) error {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return err
		}

		for _, e := range entries {
			eAbs := filepath.Join(abs, e.Name())
			eRel := rel + "/" + e.Name()

			switch {
			case rel == vendorRel && e.Name() == StagingDirName:
				// Never an extra (spec §5.3).
			case owned[eRel]:
				// A locked dest owns this subtree.
			case e.IsDir() && ownsBelow(eRel, owned):
				if err := walk(eAbs, eRel); err != nil {
					return err
				}
			default:
				if err := gitrun.RemoveAll(eAbs); err != nil {
					return err
				}

				removed = append(removed, eRel)
			}
		}

		return nil
	}

	if err := walk(vendorAbs, vendorRel); err != nil {
		return nil, err
	}

	return removed, nil
}

// ownsBelow reports whether any owned dest lies strictly below dir.
func ownsBelow(dir string, owned map[string]bool) bool {
	for dest := range owned {
		if strings.HasPrefix(dest, dir+"/") {
			return true
		}
	}

	return false
}

// copyTree copies the file tree at src to dst, preserving file modes — the
// cross-filesystem fallback for custom dests.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755) //nolint:gosec // Vendor trees are world-readable by design.
		}

		return copyFile(p, target, info.Mode())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // The path comes from walking the staged tree.
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // Read-only file.

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm()) //nolint:gosec // Same as above.
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close() //nolint:errcheck,gosec // The copy error wins.

		return err
	}

	return out.Close()
}
