// Copyright 2026 The Graft Authors

// Package fetcher fetches a locked commit from a remote repository and
// materializes its checked-out tree — without the .git directory — for
// hashing and installation (spec §5.3, §3.2 normalization).
package fetcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gitrun"
	"github.com/min0625/graft/internal/hasher"
	"github.com/min0625/graft/internal/repocache"
)

// Fetch checks out the tree of commit from repo into dst, which must not
// exist yet; its parent directory is used for staging, so dst appears
// atomically. When path is non-empty only that subdirectory of the repo
// becomes dst. The returned time is the committer timestamp of commit; the
// returned string slice lists the symlink paths that were skipped (empty
// unless skipSymlinks is set), for the caller's warnings.
//
// The commit is fetched into repo's shared bare cache under cacheRoot (spec
// §5.4) — incrementally, with the three-step fallback of §5.3 — then checked
// out locally. version, when a tag, enables the middle fallback step. name is
// the dependency name, used in error messages. The checkout forces
// core.autocrlf=false, core.eol=lf, core.symlinks=false, and
// core.longpaths=true, and never writes a .git directory, so identical bytes
// land on every platform (spec §3.2, §5.3).
//
// Tree policy is enforced from the git tree-object modes before checkout, so
// it is identical on every platform (spec §3.2): a git-submodule entry (mode
// 160000) is rejected with exit 2; a symlink (mode 120000) is rejected with
// exit 2 unless skipSymlinks is set, in which case its checked-out
// placeholder file is deleted from the tree; path portability and collision
// validation run on the tree paths. Trees that use Git LFS are rejected with
// exit 2 (unsupported in v1).
func Fetch(
	ctx context.Context,
	cacheRoot, name, repo, commit, version, path, dst string,
	skipSymlinks bool,
) (time.Time, []string, error) {
	bare, err := repocache.EnsureCommit(ctx, cacheRoot, repo, commit, version, path)
	if err != nil {
		return time.Time{}, nil, err
	}

	if path != "" {
		ok, err := repocache.PathExists(ctx, bare, commit, path)
		if err != nil {
			return time.Time{}, nil, err
		}

		if !ok {
			return time.Time{}, nil, clierr.New(clierr.CodeConfig,
				fmt.Sprintf("subdir %q of dependency %q not found at commit %.12s", path, name, commit),
				"an empty or missing subdir is almost always a typo — check the manifest",
			)
		}
	}

	// Read the tree from the git object database before anything is checked
	// out, so every policy decision below is independent of the platform and
	// filesystem (spec §3.2, decision §10.14).
	tree, err := repocache.TreeEntries(ctx, bare, commit, path)
	if err != nil {
		return time.Time{}, nil, err
	}

	if len(tree.Gitlinks) > 0 {
		return time.Time{}, nil, clierr.New(clierr.CodeConfig,
			fmt.Sprintf("dependency %q contains a git submodule at %q", name, tree.Gitlinks[0]),
			"a plain checkout would silently omit the submodule's content",
			"vendor the submodule's repository as its own graft dependency instead",
		)
	}

	if !skipSymlinks && len(tree.Symlinks) > 0 {
		return time.Time{}, nil, hasher.SymlinkRejectError(tree.Symlinks[0])
	}

	if err := hasher.ValidateTreePaths(tree.Paths); err != nil {
		return time.Time{}, nil, err
	}

	scratch, err := os.MkdirTemp(filepath.Dir(dst), ".graft-checkout-*")
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("create checkout staging dir: %w", err)
	}
	defer gitrun.RemoveAll(scratch) //nolint:errcheck // Best-effort staging cleanup.

	if err := repocache.Checkout(ctx, bare, commit, path, scratch); err != nil {
		return time.Time{}, nil, err
	}

	commitTime, err := repocache.CommitTime(ctx, bare, commit)
	if err != nil {
		return time.Time{}, nil, err
	}

	treeRoot := scratch
	if path != "" {
		treeRoot = filepath.Join(scratch, filepath.FromSlash(path))

		if info, err := os.Stat(treeRoot); err != nil || !info.IsDir() {
			return time.Time{}, nil, clierr.New(clierr.CodeConfig,
				fmt.Sprintf("subdir %q of dependency %q is not a directory at commit %.12s", path, name, commit),
				"a subdir must point at a directory in the dependency repo — check the manifest",
			)
		}
	}

	// With core.symlinks=false a skipped symlink checks out as a plain
	// placeholder file; delete it by tree path so it never reaches the hash,
	// the store, or the vendor directory — on every platform (spec §3.2).
	for _, rel := range tree.Symlinks {
		if err := os.Remove(filepath.Join(treeRoot, filepath.FromSlash(rel))); err != nil &&
			!errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, nil, fmt.Errorf("remove skipped symlink %q: %w", rel, err)
		}
	}

	if err := rejectLFS(name, treeRoot); err != nil {
		return time.Time{}, nil, err
	}

	if err := os.Rename(treeRoot, dst); err != nil {
		return time.Time{}, nil, fmt.Errorf("move checked-out tree into place: %w", err)
	}

	if err := hasher.WriteExecBits(dst, tree.ExecBits); err != nil {
		return time.Time{}, nil, fmt.Errorf("write exec-bits metadata: %w", err)
	}

	return commitTime, tree.Symlinks, nil
}

// rejectLFS fails with exit 2 when any .gitattributes in the tree declares
// the lfs filter: a plain git checkout would pin pointer files instead of
// content (spec §5.3).
func rejectLFS(name, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}

			return nil
		}

		if d.Name() != ".gitattributes" {
			return nil
		}

		usesLFS, err := declaresLFSFilter(p)
		if err != nil {
			return err
		}

		if usesLFS {
			return clierr.New(
				clierr.CodeConfig,
				fmt.Sprintf("dependency %q uses Git LFS", name),
				"graft v1 checks out trees with plain git, which would silently pin\nLFS pointer files instead of the real content",
				"Git LFS is not supported — vendor this dependency another way",
			)
		}

		return nil
	})
}

func declaresLFSFilter(path string) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // The path comes from walking the fetched tree.
	if err != nil {
		return false, err
	}
	defer f.Close() //nolint:errcheck // Read-only file.

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		for field := range strings.FieldsSeq(line) {
			if field == "filter=lfs" {
				return true, nil
			}
		}
	}

	return false, scanner.Err()
}
