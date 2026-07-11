// Copyright 2026 The Graft Authors

// Package vendordir reconciles the on-disk vendor state with the lockfile
// (spec §5.1): missing deps are installed, extras removed, and mismatched
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
	"syscall"

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
	// phase (spec §5.2).
	defaultFetchJobs = 16
	// windowsGOOS is runtime.GOOS's value on Windows, factored out since
	// several platform-specific checks in this file compare against it.
	windowsGOOS = "windows"
	// backupSuffix names where restoreParked leaves a parked dest it could not
	// rename back into place, so the tree survives the unconditional staging
	// cleanup at the end of Reconcile instead of being deleted along with it.
	// It is a transient artifact, not a feature: FindExtras does not recognize
	// it as owned by any dep, so the next apply reports and removes it like
	// any other surplus path.
	backupSuffix = ".graft-backup"
)

// FetchFunc materializes the tree of dep into dst, which does not exist yet
// but whose parent directory does.
type FetchFunc func(ctx context.Context, dep lockfile.LockedDep, dst string) error

// Mode is how a store entry becomes a dest (spec §5.4). It is a machine-local
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

	stagingLabel := path.Join(vendorDir, StagingDirName)

	// Clean staging left behind by an interrupted run (spec §5.1).
	if err := removeStaging(stagingLabel, staging, "clean stale staging"); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(staging, 0o755); err != nil { //nolint:gosec // Vendor trees are world-readable by design.
		return nil, fmt.Errorf("create staging dir: %w", err)
	}

	// A failed reconcile must not leave staging (or an otherwise-empty vendor
	// root) behind; the success path repeats the staging removal so it can
	// report the error.
	defer func() {
		// Same guard as removeStaging: on POSIX this RemoveAll would succeed
		// even with the cwd inside staging, stranding the caller's shell right
		// after an error told it to cd out and re-run.
		if cwdInsideErr("remove", stagingLabel, staging) == nil {
			gitrun.RemoveAll(staging) //nolint:errcheck,gosec // Best-effort cleanup on error paths.
		}

		os.Remove(vendorAbs) //nolint:errcheck,gosec // Only succeeds on an empty directory.
	}()

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

	if err := removeStaging(stagingLabel, staging, "remove staging dir"); err != nil {
		return nil, err
	}

	return &result, nil
}

// removeStaging checks that the process's cwd is not inside staging, then
// removes it — the "check then remove staging" sequence Reconcile runs both
// before an interrupted-run cleanup and after a successful reconcile, wrapping
// the removal error with wrapMsg.
func removeStaging(stagingLabel, staging, wrapMsg string) error {
	if err := cwdInsideErr("remove", stagingLabel, staging); err != nil {
		return err
	}

	if err := gitrun.RemoveAll(staging); err != nil {
		return fmt.Errorf("%s: %w", wrapMsg, err)
	}

	return nil
}

// fetchResult is the outcome of the fetch phase for one dep.
type fetchResult struct {
	storePath string // content-store path; empty when skip is true or err is set
	skip      bool   // dest already matches the locked hash — install phase is skipped
	err       error
}

// fetchDep is the fetch-phase worker for one dep (spec §5.2). It checks
// whether the dest is already up-to-date and returns skip=true if so;
// otherwise it ensures the dep's tree is present in the content store.
func fetchDep(
	ctx context.Context,
	root, vendorDir string,
	dep lockfile.LockedDep,
	opts Options,
) fetchResult {
	destAbs := filepath.Join(root, filepath.FromSlash(path.Join(vendorDir, dep.Name)))

	if opts.Mode == ModeLink {
		storePath := store.Path(opts.StoreRoot, dep.Hash)
		if LinkMatches(destAbs, storePath) && store.Exists(opts.StoreRoot, dep.Hash) {
			return fetchResult{skip: true}
		}

		sp, err := ensureStored(ctx, dep, opts)

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

	sp, err := ensureStored(ctx, dep, opts)

	return fetchResult{storePath: sp, err: err}
}

// installDep is the install-phase worker for one dep (spec §5.2). It
// materializes the already-stored tree at storePath into destAbs.
func installDep(staging, destAbs string, dep lockfile.LockedDep, seq int, opts Options, storePath string) error {
	if opts.Mode == ModeLink {
		return installLinkDep(staging, destAbs, dep, seq, opts, storePath)
	}

	if err := install(staging, storePath, destAbs, dep, seq); err != nil {
		return fmt.Errorf("install %q: %w", dep.Name, err)
	}

	return nil
}

// installLinkDep is installDep's ModeLink branch: it builds a symlink or
// junction pointing at storePath and swaps it into destAbs.
func installLinkDep(staging, destAbs string, dep lockfile.LockedDep, seq int, opts Options, storePath string) error {
	// Build the link (or junction) off to the side first, so a dest that
	// cannot be replaced (see parkExisting) is never left cleared with
	// nothing installed in its place.
	link := filepath.Join(staging, "link-"+strconv.Itoa(seq))
	if err := linkDir(storePath, link); err != nil {
		return fmt.Errorf("link %q: %w", dep.Name, err)
	}

	old, err := prepareSwap(staging, destAbs, dep.Name, seq)
	if err != nil {
		return fmt.Errorf("link %q: %w", dep.Name, err)
	}

	// staging is normally a sibling of destAbs, so this rename is normally
	// same-filesystem — but if destAbs is mounted separately (the same
	// "custom dir mounted separately from the vendor root" case parkExisting
	// itself falls back for), the rename can still hit EXDEV. Unlike install's
	// plain-file rename, there is no copyTree fallback: copying a
	// symlink/junction node-by-node would be wrong. Instead, build the link
	// directly at destAbs — linkDir has no same-filesystem requirement.
	if err := renameFile(link, destAbs); err != nil {
		if linkErr := linkDir(storePath, destAbs); linkErr != nil {
			restoreParked(old, destAbs)

			return fmt.Errorf("link %q: move into place: %w", dep.Name, errors.Join(err, linkErr))
		}

		gitrun.RemoveAll(link) //nolint:errcheck,gosec // Best-effort; destAbs now holds the link.
	}

	return links.Register(opts.LinksDir, destAbs, dep.Hash)
}

// reconcileDeps runs the two-phase reconcile for every dep (spec §5.2).
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
// lockfile order, so one run surfaces every failure at once (spec §5.2).
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
					fetchResults[i] = fetchDep(ctx, root, vendorDir, deps[i], opts)
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
// pointing at storePath, the cheap link-mode validation of spec §5.4.
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

// isLinkNode reports whether path is a symlink or Windows junction, detected
// by Readlink succeeding — the same way LinkMatches does. An Lstat mode-bit
// check would miss junctions: since Go 1.23 (winsymlink=1) a junction's mode
// is ModeIrregular, not ModeSymlink.
func isLinkNode(path string) bool {
	_, err := os.Readlink(path)

	return err == nil
}

// ensureStored returns the store path for dep's locked content, fetching and
// verifying it on a store miss. A fetched tree whose hash does not match the
// lockfile is an exit-4 integrity failure (spec §5.1).
func ensureStored(ctx context.Context, dep lockfile.LockedDep, opts Options) (string, error) {
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

// corruptedStoreErr is the exit-4 error for a content store entry that no
// longer hashes to its key. The entry has already been removed, so a plain
// re-run re-fetches it.
func corruptedStoreErr(dep lockfile.LockedDep, got string) error {
	return clierr.New(clierr.CodeIntegrity,
		fmt.Sprintf("cached content for %q is corrupted", dep.Name),
		"expected  "+dep.Hash+"\ngot       "+got,
		"the global cache held an entry that no longer matches its hash — it was\n"+
			"removed, nothing was installed from it\n"+
			"run `graft apply` again to re-fetch, and `graft cache verify` to check\n"+
			"the rest of the store",
	)
}

// install materializes the store entry at storePath into destAbs: the entry
// is first copied (reflink where supported) into the vendor staging area,
// re-hashed against the locked hash, the old dest (if any) is parked, and the
// staged copy is renamed into place — an atomic rename in the common case,
// with a copy fallback when the staging area and dest are on different
// filesystems. The shared store entry itself is never moved.
func install(staging, storePath, destAbs string, dep lockfile.LockedDep, seq int) error {
	stage := filepath.Join(staging, "new-"+strconv.Itoa(seq))
	if err := store.Materialize(storePath, stage); err != nil {
		// A parallel install of the same hash may have found the entry
		// corrupted and removed it mid-materialize (see the re-hash below);
		// report that as the integrity failure it is, not a raw I/O error.
		if _, statErr := os.Stat(storePath); os.IsNotExist(statErr) {
			return corruptedStoreErr(dep, "(entry removed by a concurrent install)")
		}

		return err
	}

	// Store entries are verified at insert and kept read-only, but the vendor
	// tree never trusts one blindly (spec §5.4): re-hash the staged copy so a
	// corrupted entry fails as exit 4 instead of being installed silently.
	got, err := hasher.HashTree(stage)
	if err != nil {
		return err
	}

	if got != dep.Hash {
		// Drop the corrupted entry so the next run re-fetches it.
		gitrun.RemoveAll(storePath) //nolint:errcheck,gosec // Best-effort; the integrity error wins.

		return corruptedStoreErr(dep, got)
	}

	old, err := prepareSwap(staging, destAbs, dep.Name, seq)
	if err != nil {
		return err
	}

	if err := renameFile(stage, destAbs); err == nil {
		return nil
	}

	if err := copyTree(stage, destAbs); err != nil {
		restoreParked(old, destAbs)

		return err
	}

	return gitrun.RemoveAll(stage)
}

// renameFile is os.Rename, indirected so tests can simulate a cross-device
// rename failure (EXDEV) without requiring a real filesystem boundary.
var renameFile = os.Rename

// lstatPath is os.Lstat, indirected so tests can simulate a non-"not exist"
// stat failure deterministically — the OS error a blocked path component
// actually produces (e.g. ENOTDIR vs. Windows' ERROR_PATH_NOT_FOUND, which
// os.IsNotExist treats the same as a genuine missing file) is not portable.
var lstatPath = os.Lstat

// prepareSwap creates dest's parent directory and parks any existing dest
// aside (see parkExisting), so the caller can then move new content into
// destAbs and, if that fails, restore the parked dest via restoreParked.
// Shared by install (copy mode) and installDep's link-mode branch, which
// otherwise duplicate this same mkdir-then-park sequence.
func prepareSwap(staging, destAbs, label string, seq int) (string, error) {
	//nolint:gosec // Vendor trees are world-readable by design.
	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return "", err
	}

	return parkExisting(staging, destAbs, label, seq)
}

// parkExisting moves any existing dest aside into staging (a rename, so it
// stays fully intact) and returns the path it was parked to, so the caller's
// own new→dest rename never has to remove the old tree first and can restore
// it (via restoreParked) if that rename then fails. If dest doesn't yet exist
// this is a no-op (empty path, nil error). A symlink or junction dest (link
// mode) is parked without the cwd check below: replacing a link node cannot
// conflict with a process whose cwd resolves through it, since that cwd
// refers to the link's target, never the link itself.
//
// If the rename fails because dest is on a different filesystem than staging
// (a custom dest mounted separately from the vendor root), there is nothing
// to park aside — dest is deleted in place instead, exactly as a plain
// install would if it were being cleared. Any other rename failure — a
// locked file on Windows is the common case, since POSIX permits renaming a
// directory with open handles inside it — returns an error without touching
// dest, so a currently-valid tree is never partially deleted while the
// replacement isn't ready yet (the next `graft apply` simply retries once the
// lock clears).
func parkExisting(staging, dest, label string, seq int) (string, error) {
	if _, err := lstatPath(dest); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}

		return "", fmt.Errorf("stat existing tree: %w", err)
	}

	if !isLinkNode(dest) {
		if err := cwdInsideErr("replace", label, dest); err != nil {
			return "", err
		}
	}

	old := filepath.Join(staging, "old-"+strconv.Itoa(seq))
	if err := renameFile(dest, old); err != nil {
		if isCrossDevice(err) {
			if err := gitrun.RemoveAll(dest); err != nil {
				return "", fmt.Errorf("clear existing tree: %w", err)
			}

			return "", nil
		}

		return "", fmt.Errorf("move existing tree aside: %w", err)
	}

	return old, nil
}

// errnoNotSameDeviceWindows is ERROR_NOT_SAME_DEVICE, the real error
// MoveFileEx returns for a cross-volume rename on Windows. Go's
// syscall.EXDEV is a synthetic POSIX-compatibility value on Windows (built
// from APPLICATION_ERROR) that a real MoveFileEx failure never produces, so
// errors.Is(err, syscall.EXDEV) alone never matches there — this constant
// closes that gap.
const errnoNotSameDeviceWindows = 17

// isCrossDevice reports whether err is a rename failure because its source
// and destination are on different filesystems/volumes.
func isCrossDevice(err error) bool {
	if errors.Is(err, syscall.EXDEV) {
		return true
	}

	var errno syscall.Errno

	return runtime.GOOS == windowsGOOS && errors.As(err, &errno) && errno == errnoNotSameDeviceWindows
}

// restoreParked puts a dest parkExisting moved aside back in place, so a
// swap-in failure after a successful park (the new tree could not be renamed
// or copied into dest) leaves the previously-valid tree installed instead of
// losing it when staging is later cleaned up. old is empty when nothing was
// parked, in which case this is a no-op. Best effort: the swap-in error is
// what the caller reports either way.
func restoreParked(old, dest string) {
	if old == "" {
		return
	}

	// A failed copy fallback may have left dest as a partial, non-empty
	// tree, which would block renaming old back over it — clear it first.
	gitrun.RemoveAll(dest) //nolint:errcheck,gosec // Best-effort restore.

	if err := renameFile(old, dest); err == nil {
		return
	}

	// The restore rename itself failed (e.g. dest is still locked on
	// Windows). old is inside staging, which Reconcile unconditionally wipes
	// once it returns, so the only intact copy of the previously-installed
	// tree would otherwise be lost silently. Move it beside dest instead —
	// this rename never touches dest, so it isn't blocked by whatever is
	// locking dest. A stale backup left by a previous failed restore would
	// block this rename (renaming onto an existing directory fails on every
	// OS), and a failing install returns before removeExtras ever reaps it —
	// it is by definition older than the tree being saved now, so clear it.
	gitrun.RemoveAll(dest + backupSuffix) //nolint:errcheck,gosec // Best-effort.
	renameFile(old, dest+backupSuffix)    //nolint:errcheck,gosec // Best-effort; nothing more we can do.
}

// cwdInsideErr reports a clear error when the process's current working
// directory is at or inside target, instead of letting the caller attempt a
// rename/delete that Windows refuses for a directory that is (or contains)
// the process's own cwd — a restriction POSIX does not have. The check
// itself is deliberately not platform-gated: it also blocks the same
// rename/delete on POSIX, so behavior does not depend on which OS graft runs
// on, and a caller is never left with its own cwd stranded in a directory
// that was just deleted out from under it. Best effort: a Getwd failure just
// skips the check, and on Windows and macOS — both case-insensitive by
// default — the comparison is case-insensitive to match the filesystem; a
// residual case or path-alias mismatch only forgoes the clearer message,
// falling back to the raw OS error. action names what the caller is about to
// do to target ("replace" for parkExisting, "remove" for removeExtras) so the
// message matches the actual operation.
func cwdInsideErr(action, label, target string) error {
	wd, err := os.Getwd()
	if err != nil {
		return nil //nolint:nilerr // Best-effort check; a real problem surfaces from the operation itself.
	}

	wd, target = filepath.Clean(wd), filepath.Clean(target)

	if runtime.GOOS == windowsGOOS || runtime.GOOS == "darwin" {
		wd, target = strings.ToLower(wd), strings.ToLower(target)
	}

	if wd != target && !strings.HasPrefix(wd, target+string(filepath.Separator)) {
		return nil
	}

	return clierr.New(clierr.CodeGeneral,
		fmt.Sprintf("cannot %s %q: current directory is inside it", action, label),
		"cd out of it first, then re-run",
	)
}

// FindExtras returns the paths under vendorDir that no locked dep owns —
// excluding the staging directory and the locked dests themselves — relative to
// the project root and slash-separated. It is the single source of truth for
// "what is an extra" (spec §5.1), shared by the destructive reconcile
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
				// Never an extra (spec §5.1).
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
		abs := filepath.Join(root, filepath.FromSlash(rel))

		// A link node is exempt for the same reason as in parkExisting: a cwd
		// resolving through it refers to the link's target, which removing
		// the link itself never disturbs.
		if !isLinkNode(abs) {
			if err := cwdInsideErr("remove", rel, abs); err != nil {
				return nil, err
			}
		}

		if err := gitrun.RemoveAll(abs); err != nil {
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

		// Store trees are normally symlink-free (hashing rejects them, skip
		// removes them), but replicate any that slip through rather than
		// following the link and copying its target's bytes.
		if info.Mode()&fs.ModeSymlink != 0 {
			dest, err := os.Readlink(p)
			if err != nil {
				return err
			}

			return os.Symlink(dest, target) //nolint:gosec // dest comes from store tree, not user input.
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
