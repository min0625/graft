// Copyright 2026 The Graft Authors

// Package vendordir reconciles the on-disk vendor state with the lockfile
// (spec §5.3): missing deps are installed, extras removed, and mismatched
// trees replaced, with every install staged in <vendor>/.graft-tmp and moved
// into place by an atomic same-filesystem rename.
package vendordir

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gitrun"
	"github.com/min0625/graft/internal/hasher"
	"github.com/min0625/graft/internal/links"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/min0625/graft/internal/store"
)

const (
	// StagingDirName is the staging directory under the vendor root. It is never
	// treated as an extra dep, and stale leftovers in it are deleted at the
	// start of every reconcile.
	StagingDirName = ".graft-tmp"
	// defaultFetchJobs is the default number of concurrent workers in the fetch
	// phase (spec §5.4).
	defaultFetchJobs = 16
)

// FetchFunc materializes the tree of dep into dst, which does not exist yet
// but whose parent directory does.
type FetchFunc func(ctx context.Context, dep lockfile.LockedDep, dst string) error

// Mode is how a store entry becomes a dest (spec §5.6). It is a machine-local
// choice, never recorded in graft.toml or graft.lock.
type Mode int

const (
	// ModeCopy copies (reflinks where possible) the store entry into the dest;
	// the default, compatible with committing the vendor directory.
	ModeCopy Mode = iota
	// ModeLink points the dest at the store entry with a directory symlink
	// (a junction on Windows). Requires the vendor directory to be gitignored.
	ModeLink
)

// Options carries the cache wiring a reconcile needs: the content store it
// materializes from, the same-filesystem checkout staging directory it fetches
// into on a store miss, the fetch function itself, and the materialization
// mode.
type Options struct {
	// StoreRoot is the content-addressed store root (<cache>/store).
	StoreRoot string
	// TmpDir is the checkout staging directory (<cache>/tmp), on the same
	// filesystem as StoreRoot so a verified tree renames atomically into it.
	TmpDir string
	// LinksDir is the link-mode registry (<cache>/links); required in ModeLink.
	LinksDir string
	// Fetch materializes a dep's tree into a given destination on a store miss.
	Fetch FetchFunc
	// Mode selects copy (default) or link materialization.
	Mode Mode
	// SkipSymlinks lists dep names whose symlinks should be silently removed
	// before hashing and storing (symlinks = "skip" in graft.toml).
	SkipSymlinks map[string]bool
}

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
// by no locked dest are removed.
func Reconcile(
	ctx context.Context,
	root, vendorDir string,
	deps []lockfile.LockedDep,
	opts Options,
) (*Result, error) {
	vendorAbs := filepath.Join(root, filepath.FromSlash(vendorDir))
	staging := filepath.Join(vendorAbs, StagingDirName)

	// A non-directory sitting where the vendor root belongs makes every staging
	// path operation fail with a confusing low-level error; report it clearly.
	if info, err := os.Stat(vendorAbs); err == nil && !info.IsDir() {
		return nil, clierr.New(clierr.CodeGeneral,
			fmt.Sprintf("vendor path %q is not a directory", vendorDir),
			"remove or rename it — graft needs this path to hold the installed dependencies",
		)
	}

	// Clean staging left behind by an interrupted run (spec §5.3).
	if err := gitrun.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("clean stale staging: %w", err)
	}

	if err := os.MkdirAll(staging, 0o755); err != nil { //nolint:gosec // Vendor trees are world-readable by design.
		return nil, fmt.Errorf("create staging dir: %w", err)
	}

	var result Result

	installed, err := reconcileDeps(ctx, root, vendorDir, staging, deps, opts)
	if err != nil {
		return nil, err
	}

	for i, dep := range deps {
		if installed[i] {
			result.Installed = append(result.Installed, dep)
		}
	}

	removed, err := removeExtras(root, vendorDir, deps)
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

// fetchResult is the outcome of the fetch phase for one dep.
type fetchResult struct {
	storePath string // content-store path; empty when skip is true or err is set
	skip      bool   // dest already matches the locked hash — install phase is skipped
	err       error
}

// fetchDep is the fetch-phase worker for one dep (spec §5.4). It checks
// whether the dest is already up-to-date and returns skip=true if so;
// otherwise it ensures the dep's tree is present in the content store.
func fetchDep(
	ctx context.Context,
	root, vendorDir string,
	dep lockfile.LockedDep,
	opts Options,
	skipSymlinks bool,
) fetchResult {
	destAbs := filepath.Join(root, filepath.FromSlash(path.Join(vendorDir, dep.Name)))

	if opts.Mode == ModeLink {
		storePath := store.Path(opts.StoreRoot, dep.Hash)
		if LinkMatches(destAbs, storePath) && store.Exists(opts.StoreRoot, dep.Hash) {
			return fetchResult{skip: true}
		}

		sp, err := ensureStored(ctx, dep, opts, skipSymlinks)

		return fetchResult{storePath: sp, err: err}
	}

	// Copy mode: skip when the dest already holds the locked content hash.
	// `graft apply` re-verifies even an existing dest (spec §10.2); any
	// divergence — including a hand-edit or a link-mode leftover — is
	// repaired by reinstalling from the store.
	if _, err := os.Lstat(destAbs); err == nil {
		if got, err := hasher.HashTree(destAbs); err == nil && got == dep.Hash {
			return fetchResult{skip: true}
		}
	}

	sp, err := ensureStored(ctx, dep, opts, skipSymlinks)

	return fetchResult{storePath: sp, err: err}
}

// installDep is the install-phase worker for one dep (spec §5.4). It
// materializes the already-stored tree at storePath into destAbs.
func installDep(staging, destAbs string, dep lockfile.LockedDep, seq int, opts Options, storePath string) error {
	if opts.Mode == ModeLink {
		// Replace whatever is at dest (a copy-mode tree, a stale or wrong link).
		if err := gitrun.RemoveAll(destAbs); err != nil {
			return fmt.Errorf("clear dest of %q: %w", dep.Name, err)
		}

		//nolint:gosec // Vendor trees are world-readable by design.
		if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
			return err
		}

		if err := linkDir(storePath, destAbs); err != nil {
			return fmt.Errorf("link %q: %w", dep.Name, err)
		}

		return links.Register(opts.LinksDir, destAbs, dep.Hash)
	}

	if err := install(staging, storePath, destAbs, seq); err != nil {
		return fmt.Errorf("install %q: %w", dep.Name, err)
	}

	return nil
}

// reconcileDeps runs the two-phase reconcile for every dep (spec §5.4).
//
// Phase 1 — fetch (high concurrency, network-bound): each worker calls
// fetchDep, which checks whether the dest is already current, and on a miss
// fetches and stores the tree. The worker count defaults to defaultFetchJobs
// so that slow network and multiple dependencies do not serialize on CPU count.
//
// Phase 2 — install (NumCPU concurrency, CPU/IO-bound): each worker calls
// installDep, which moves the prepared store tree into the vendor directory.
//
// Errors from both phases are collected — not fail-fast — and joined in
// lockfile order, so one run surfaces every failure at once (spec §5.4).
func reconcileDeps(
	ctx context.Context,
	root, vendorDir, staging string,
	deps []lockfile.LockedDep,
	opts Options,
) ([]bool, error) {
	if len(deps) == 0 {
		return nil, nil
	}

	fetchWorkers, installWorkers := defaultFetchJobs, runtime.NumCPU()

	// Phase 1: fetch / store (high concurrency).
	fetchResults := make([]fetchResult, len(deps))

	{
		jobs := make(chan int)

		var wg sync.WaitGroup

		for range min(len(deps), fetchWorkers) {
			wg.Go(func() {
				for i := range jobs {
					fetchResults[i] = fetchDep(ctx, root, vendorDir, deps[i], opts, opts.SkipSymlinks[deps[i].Name])
				}
			})
		}

		for i := range deps {
			jobs <- i
		}

		close(jobs)
		wg.Wait()
	}

	// Phase 2: install / materialise (NumCPU concurrency).
	// Deps whose fetch phase failed are skipped — their error surfaces below.
	installed := make([]bool, len(deps))
	installErrs := make([]error, len(deps))

	{
		jobs := make(chan int)

		var wg sync.WaitGroup

		for range min(len(deps), installWorkers) {
			wg.Go(func() {
				for i := range jobs {
					r := fetchResults[i]
					if r.skip || r.err != nil {
						continue
					}

					destAbs := filepath.Join(root, filepath.FromSlash(path.Join(vendorDir, deps[i].Name)))
					if err := installDep(staging, destAbs, deps[i], i, opts, r.storePath); err != nil {
						installErrs[i] = err
						continue
					}

					installed[i] = true
				}
			})
		}

		for i := range deps {
			jobs <- i
		}

		close(jobs)
		wg.Wait()
	}

	// Join errors from both phases in lockfile order.
	allErrs := make([]error, 0, 2*len(deps))
	for _, r := range fetchResults {
		allErrs = append(allErrs, r.err)
	}

	allErrs = append(allErrs, installErrs...)

	return installed, errors.Join(allErrs...)
}

// LinkMatches reports whether destAbs is a symlink (or Windows junction)
// pointing at storePath, the cheap link-mode validation of spec §5.6.
func LinkMatches(destAbs, storePath string) bool {
	target, err := os.Readlink(destAbs)
	if err != nil {
		return false
	}

	return cleanLinkTarget(target) == cleanLinkTarget(storePath)
}

// cleanLinkTarget normalizes a link target for comparison, stripping the
// Windows extended-length prefix a junction's target may carry.
func cleanLinkTarget(target string) string {
	target = strings.TrimPrefix(target, `\\?\`)

	return filepath.Clean(target)
}

// ensureStored returns the store path for dep's locked content, fetching and
// verifying it on a store miss. A fetched tree whose hash does not match the
// lockfile is an exit-4 integrity failure (spec §5.3).
func ensureStored(ctx context.Context, dep lockfile.LockedDep, opts Options, skipSymlinks bool) (string, error) {
	if store.Exists(opts.StoreRoot, dep.Hash) {
		return store.Path(opts.StoreRoot, dep.Hash), nil
	}

	holder, err := os.MkdirTemp(opts.TmpDir, "checkout-*")
	if err != nil {
		return "", fmt.Errorf("create checkout staging dir: %w", err)
	}
	defer gitrun.RemoveAll(holder) //nolint:errcheck // Best-effort staging cleanup.

	fetchDst := filepath.Join(holder, "tree")

	if err := opts.Fetch(ctx, dep, fetchDst); err != nil {
		return "", err
	}

	if skipSymlinks {
		// The per-symlink warning is emitted once at add/lock time (see relock.go);
		// apply is routine/CI and deliberately stays quiet, so the skipped paths
		// are discarded here.
		if _, err := hasher.RemoveSymlinks(fetchDst); err != nil {
			return "", fmt.Errorf("remove symlinks from %q: %w", dep.Name, err)
		}
	}

	got, err := hasher.HashTree(fetchDst)
	if err != nil {
		return "", err
	}

	if got != dep.Hash {
		return "", integrityErr(dep, got)
	}

	return store.Insert(opts.StoreRoot, fetchDst, dep.Hash)
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

// install materializes the store entry at storePath into destAbs: the entry
// is first copied (reflink where supported) into the vendor staging area, the
// old dest (if any) is parked, and the staged copy is renamed into place — an
// atomic rename in the common case, with a copy fallback when the staging area
// and dest are on different filesystems. The shared store entry itself is never
// moved.
func install(staging, storePath, destAbs string, seq int) error {
	stage := filepath.Join(staging, "new-"+strconv.Itoa(seq))
	if err := store.Materialize(storePath, stage); err != nil {
		return err
	}

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

	if err := os.Rename(stage, destAbs); err == nil {
		return nil
	}

	if err := copyTree(stage, destAbs); err != nil {
		return err
	}

	return gitrun.RemoveAll(stage)
}

// FindExtras returns the paths under vendorDir that no locked dep owns —
// excluding the staging directory and the locked dests themselves — relative to
// the project root and slash-separated. It is the single source of truth for
// "what is an extra" (spec §5.3), shared by the destructive reconcile
// (removeExtras) and the read-only status command.
func FindExtras(root, vendorDir string, deps []lockfile.LockedDep) ([]string, error) {
	vendorAbs := filepath.Join(root, filepath.FromSlash(vendorDir))

	owned := make(map[string]bool, len(deps))
	for _, dep := range deps {
		owned[path.Join(vendorDir, dep.Name)] = true
	}

	if _, err := os.Stat(vendorAbs); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var extras []string

	var walk func(abs, rel string) error

	walk = func(abs, rel string) error {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return err
		}

		for _, e := range entries {
			eRel := rel + "/" + e.Name()

			switch {
			case rel == vendorDir && e.Name() == StagingDirName:
				// Never an extra (spec §5.3).
			case owned[eRel]:
				// A locked dest owns this subtree.
			case e.IsDir() && ownsBelow(eRel, owned):
				if err := walk(filepath.Join(abs, e.Name()), eRel); err != nil {
					return err
				}
			default:
				extras = append(extras, eRel)
			}
		}

		return nil
	}

	if err := walk(vendorAbs, vendorDir); err != nil {
		return nil, err
	}

	return extras, nil
}

// removeExtras deletes every path FindExtras reports as unowned under the
// vendor directory and returns the removed paths (relative to the project root,
// slash-separated).
func removeExtras(root, vendorDir string, deps []lockfile.LockedDep) ([]string, error) {
	extras, err := FindExtras(root, vendorDir, deps)
	if err != nil {
		return nil, err
	}

	for _, rel := range extras {
		if err := gitrun.RemoveAll(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return nil, err
		}
	}

	return extras, nil
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
