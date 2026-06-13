// Copyright 2026 The Graft Authors

// Package repocache manages graft's shared bare-repository cache (spec §5.6):
// every fetch targets repos/<host>/<org>/<repo>.git, keyed by the canonical
// repo form so HTTPS and SSH spellings share one entry. Fetches are
// incremental — a commit already in the cache is never re-fetched — and
// serialized per repository by an advisory file lock. The cache is purely a
// performance layer; deleting it is always safe.
package repocache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gitrun"
)

// lockPollInterval is how often a contended fetch lock is retried.
const lockPollInterval = 50 * time.Millisecond

// BarePath returns the bare-repository path for repo under cacheRoot:
// <cacheRoot>/repos/<host>/<org>/<repo>.git, keyed by the canonical repo form
// (spec §5.6, §10.8) so every spelling of the same remote shares one entry.
func BarePath(cacheRoot, repo string) string {
	canonical := gitrun.CanonicalRepo(repo)

	return filepath.Join(cacheRoot, cachedir.ReposSubdir, filepath.FromSlash(canonical)+".git")
}

// EnsureCommit makes commit available in repo's bare cache and returns the
// bare-repository path. It takes the per-repo advisory fetch lock, skips the
// fetch when the commit is already cached (incremental), and otherwise runs
// the three-step fallback of spec §5.5. version, when a tag, enables the
// middle step. For a path-scoped dep the fetch uses --filter=blob:none, so the
// bare repo becomes a partial clone and blobs outside the path are fetched
// lazily at checkout time.
func EnsureCommit(ctx context.Context, cacheRoot, repo, commit, version, path string) (string, error) {
	bare := BarePath(cacheRoot, repo)

	release, err := lock(ctx, cacheRoot, repo)
	if err != nil {
		return "", err
	}
	defer release()

	if err := ensureBare(ctx, bare, repo); err != nil {
		return "", err
	}

	// Incremental: never re-fetch a commit already in the cache (spec §5.5).
	if hasCommit(ctx, bare, commit) {
		return bare, nil
	}

	if err := fetchCommit(ctx, bare, repo, commit, version, path); err != nil {
		return "", err
	}

	if !hasCommit(ctx, bare, commit) {
		return "", commitGoneErr(repo, commit)
	}

	return bare, nil
}

// ensureBare creates the bare repository if it does not exist and configures
// the origin remote (used as the promisor for partial-clone path deps).
func ensureBare(ctx context.Context, bare, repo string) error {
	if _, err := os.Stat(bare); err == nil {
		return nil
	}

	//nolint:gosec // The cache is world-readable by design, like the vendor tree.
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		return fmt.Errorf("create bare repo directory: %w", err)
	}

	if _, err := gitrun.Run(ctx, "", "init", "--quiet", "--bare", bare); err != nil {
		return fmt.Errorf("git init --bare: %w", err)
	}

	if _, err := bareGit(ctx, bare, "remote", "add", "origin", gitrun.RemoteURL(repo)); err != nil {
		return fmt.Errorf("configure origin remote: %w", err)
	}

	return nil
}

// bareGit runs git against the bare repository at bare using an explicit
// --git-dir, so it works regardless of the user's safe.bareRepository setting
// (which, when "explicit", refuses to auto-detect a bare repo from the cwd).
func bareGit(ctx context.Context, bare string, args ...string) (string, error) {
	return gitrun.Run(ctx, "", append([]string{"--git-dir=" + bare}, args...)...)
}

// CommitTime returns the committer timestamp (UTC) of commit in the bare
// repository at bare.
func CommitTime(ctx context.Context, bare, commit string) (time.Time, error) {
	out, err := bareGit(ctx, bare, "show", "--no-patch", "--format=%cI", commit)
	if err != nil {
		return time.Time{}, fmt.Errorf("read committer time: %w", err)
	}

	t, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse committer time: %w", err)
	}

	return t.UTC(), nil
}

// fetchCommit runs the three-step fallback fetch of spec §5.5 into bare. A
// path dep filters out blobs (--filter=blob:none); the commit, its trees, and
// the path's blobs are all that ever land in the cache.
func fetchCommit(ctx context.Context, bare, repo, commit, version, path string) error {
	filtered := path != ""

	// 1. Cheapest: a depth-1 fetch of the SHA itself. Hosted platforms allow
	//    it; plain git servers may reject an unadvertised SHA.
	if fetchRef(ctx, bare, commit, filtered) == nil && hasCommit(ctx, bare, commit) {
		return nil
	}

	// 2. When the locked version is a tag, fetch the tag and see whether it
	//    still points at the locked commit. A mismatch is not an error — the
	//    tag may have been re-pointed — so fall through.
	if isTag(version) {
		if fetchRef(ctx, bare, version, filtered) == nil && hasCommit(ctx, bare, commit) {
			return nil
		}
	}

	// 3. Always-correct fallback: fetch every ref, classifying an unreachable
	//    remote as a network error (exit 3).
	return fetchAll(ctx, bare, repo, filtered)
}

// fetchRef fetches a single ref at depth 1 from origin, optionally filtering
// out blobs. The server may reject an unadvertised SHA or the filter, in which
// case the caller falls back.
func fetchRef(ctx context.Context, bare, ref string, filtered bool) error {
	args := []string{"fetch", "--quiet", "--depth=1"}
	if filtered {
		args = append(args, "--filter=blob:none")
	}

	args = append(args, "origin", ref)

	_, err := bareGit(ctx, bare, args...)

	return err
}

// fetchAll fetches every branch and tag from origin into bare, mapping the
// refs into refs/remotes and refs/tags so nothing collides. When filtered, it
// first tries a blob-filtered fetch and silently retries unfiltered if the
// server does not support partial clone (spec §5.5) — the checkout then still
// restricts the working tree to the path. An unreachable remote is reported as
// a network error (exit 3).
func fetchAll(ctx context.Context, bare, repo string, filtered bool) error {
	if filtered && fetchAllRefs(ctx, bare, true) == nil {
		return nil
	}

	if err := fetchAllRefs(ctx, bare, false); err != nil {
		if !gitrun.Reachable(ctx, repo) {
			return gitrun.NetworkErr(repo, err)
		}

		return err
	}

	return nil
}

// fetchAllRefs runs the all-refs fetch with or without the blob filter.
func fetchAllRefs(ctx context.Context, bare string, filtered bool) error {
	args := []string{"fetch", "--quiet"}
	if filtered {
		args = append(args, "--filter=blob:none")
	}

	args = append(args, "origin",
		"+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*")

	_, err := bareGit(ctx, bare, args...)

	return err
}

// hasCommit reports whether commit is present in bare as a commit object.
func hasCommit(ctx context.Context, bare, commit string) bool {
	_, err := bareGit(ctx, bare, "rev-parse", "--verify", "--quiet", commit+"^{commit}")

	return err == nil
}

// isTag reports whether a locked version string is a tag (anything that is not
// a pseudo-version), so the middle fallback step applies.
func isTag(version string) bool {
	return version != "" && !pseudoVersion(version)
}

// pseudoVersion reports whether version has the v0.0.0-<timestamp>-<sha12>
// shape produced for unmarked commits, which is not a real tag.
func pseudoVersion(version string) bool {
	const tsLen = 14 // yyyymmddhhmmss

	rest, ok := strings.CutPrefix(version, "v0.0.0-")
	if !ok {
		return false
	}

	ts, sha, ok := strings.Cut(rest, "-")

	return ok && len(ts) == tsLen && len(sha) == 12
}

// commitGoneErr is the spec §6 exit-1 error: the locked commit no longer
// exists on the remote, so its history was probably rewritten.
func commitGoneErr(repo, commit string) error {
	return clierr.New(clierr.CodeGeneral,
		fmt.Sprintf("commit %.12s not found in %s", commit, repo),
		"the locked commit no longer exists on the remote — its history may have been rewritten",
		"run `graft add <name>@<ref>` to re-pin the dependency, then commit the updated files",
	)
}

// Checkout materializes commit's tree from the bare repository at bare into
// dst, which must not exist yet. When path is non-empty only that subdirectory
// is checked out (landing at dst/<path>). The checkout forces
// core.autocrlf=false and core.eol=lf so identical bytes land on every
// platform (spec §3.2), and uses a private index so parallel checkouts of one
// bare repo never collide. dst contains no .git — the git directory stays in
// the cache.
func Checkout(ctx context.Context, bare, commit, path, dst string) error {
	bareAbs, err := filepath.Abs(bare)
	if err != nil {
		return fmt.Errorf("resolve bare repo path: %w", err)
	}

	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("resolve checkout path: %w", err)
	}

	//nolint:gosec // Vendor and cache trees are world-readable by design.
	if err := os.MkdirAll(dstAbs, 0o755); err != nil {
		return fmt.Errorf("create checkout dir: %w", err)
	}

	index := dstAbs + ".graft-index"
	defer os.Remove(index) //nolint:errcheck // Best-effort temp index cleanup.

	pathspec := "."
	if path != "" {
		pathspec = path
	}

	// cmd.Dir is the work-tree root so the pathspec resolves against it.
	_, err = gitrun.RunEnv(ctx, dstAbs, []string{"GIT_INDEX_FILE=" + index},
		"--git-dir="+bareAbs, "--work-tree="+dstAbs,
		"-c", "core.autocrlf=false", "-c", "core.eol=lf",
		"checkout", "--quiet", commit, "--", pathspec)
	if err != nil {
		return fmt.Errorf("checkout %.12s: %w", commit, err)
	}

	return nil
}

// Clean removes every cached bare repository last fetched before the given
// time, returning the removed repos' cache-relative paths in sorted order
// (spec §4.6). Removing a bare repo only costs a re-fetch; deleting the whole
// cache is always safe.
func Clean(cacheRoot string, before time.Time) ([]string, error) {
	reposDir := filepath.Join(cacheRoot, cachedir.ReposSubdir)

	var removed []string

	err := filepath.WalkDir(reposDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}

			return err
		}

		if !d.IsDir() || !strings.HasSuffix(d.Name(), ".git") {
			return nil
		}

		if lastFetch(p).Before(before) {
			if err := gitrun.RemoveAll(p); err != nil {
				return fmt.Errorf("remove cached repo: %w", err)
			}

			rel, relErr := filepath.Rel(reposDir, p)
			if relErr != nil {
				rel = p
			}

			removed = append(removed, filepath.ToSlash(rel))
		}

		// A .git directory is a leaf bare repo — never descend into it.
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(removed)

	return removed, nil
}

// lastFetch returns when bare was last fetched, using FETCH_HEAD's mtime
// (git rewrites it on every fetch) and falling back to the directory's mtime.
func lastFetch(bare string) time.Time {
	if info, err := os.Stat(filepath.Join(bare, "FETCH_HEAD")); err == nil {
		return info.ModTime()
	}

	if info, err := os.Stat(bare); err == nil {
		return info.ModTime()
	}

	return time.Time{}
}

// PathExists reports whether path is present in commit's tree in the bare
// repository. It reads only tree objects, so it works on a partial clone
// before any blob has been fetched — letting the caller report a mistyped
// path before attempting a checkout that would fail unhelpfully.
func PathExists(ctx context.Context, bare, commit, path string) (bool, error) {
	out, err := bareGit(ctx, bare, "ls-tree", commit, "--", path)
	if err != nil {
		return false, fmt.Errorf("inspect path %q: %w", path, err)
	}

	return strings.TrimSpace(out) != "", nil
}

// lock takes the exclusive advisory fetch lock for repo, serializing
// concurrent fetches of the same bare repository (spec §5.6). The lock file
// lives in <cacheRoot>/locks/repos, keyed by the canonical repo hash.
func lock(ctx context.Context, cacheRoot, repo string) (func(), error) {
	dir := filepath.Join(cacheRoot, "locks", "repos")

	//nolint:gosec // The cache is world-readable by design.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create repo lock directory: %w", err)
	}

	sum := sha256.Sum256([]byte(gitrun.CanonicalRepo(repo)))
	fl := flock.New(filepath.Join(dir, hex.EncodeToString(sum[:])))

	if _, err := fl.TryLockContext(ctx, lockPollInterval); err != nil {
		return nil, fmt.Errorf("acquire repo fetch lock: %w", err)
	}

	return func() { fl.Unlock() }, nil //nolint:errcheck,gosec // Releasing an advisory lock is best effort.
}
