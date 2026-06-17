// Copyright 2026 The Graft Authors

// Package fetcher fetches a locked commit from a remote repository and
// materializes its checked-out tree — without the .git directory — for
// hashing and installation (spec §5.5, §3.2 normalization).
package fetcher

import (
	"bufio"
	"context"
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
// becomes dst. The returned time is the committer timestamp of commit.
//
// The commit is fetched into repo's shared bare cache under cacheRoot (spec
// §5.6) — incrementally, with the three-step fallback of §5.5 — then checked
// out locally. version, when a tag, enables the middle fallback step. name is
// the dependency name, used in error messages. The checkout forces
// core.autocrlf=false and core.eol=lf and never writes a .git directory, so
// identical bytes land on every platform (spec §3.2). Trees that use Git LFS
// are rejected with exit 2 (unsupported in v1).
func Fetch(ctx context.Context, cacheRoot, name, repo, commit, version, path, dst string) (time.Time, error) {
	bare, err := repocache.EnsureCommit(ctx, cacheRoot, repo, commit, version, path)
	if err != nil {
		return time.Time{}, err
	}

	if path != "" {
		ok, err := repocache.PathExists(ctx, bare, commit, path)
		if err != nil {
			return time.Time{}, err
		}

		if !ok {
			return time.Time{}, clierr.New(clierr.CodeConfig,
				fmt.Sprintf("path %q of dependency %q not found at commit %.12s", path, name, commit),
				"an empty or missing path is almost always a mistyped `path` — check the manifest",
			)
		}
	}

	scratch, err := os.MkdirTemp(filepath.Dir(dst), ".graft-checkout-*")
	if err != nil {
		return time.Time{}, fmt.Errorf("create checkout staging dir: %w", err)
	}
	defer gitrun.RemoveAll(scratch) //nolint:errcheck // Best-effort staging cleanup.

	if err := repocache.Checkout(ctx, bare, commit, path, scratch); err != nil {
		return time.Time{}, err
	}

	commitTime, err := repocache.CommitTime(ctx, bare, commit)
	if err != nil {
		return time.Time{}, err
	}

	treeRoot := scratch
	if path != "" {
		treeRoot = filepath.Join(scratch, filepath.FromSlash(path))

		if info, err := os.Stat(treeRoot); err != nil || !info.IsDir() {
			return time.Time{}, clierr.New(clierr.CodeConfig,
				fmt.Sprintf("path %q of dependency %q not found at commit %.12s", path, name, commit),
				"an empty or missing path is almost always a mistyped `path` — check the manifest",
			)
		}
	}

	if err := rejectLFS(name, treeRoot); err != nil {
		return time.Time{}, err
	}

	// Get exec bits from the git index before moving the tree, so the
	// metadata is correct on all platforms (spec §3.2, decision §10.14).
	execBits, err := repocache.ExecBits(ctx, bare, commit, path)
	if err != nil {
		return time.Time{}, err
	}

	if err := os.Rename(treeRoot, dst); err != nil {
		return time.Time{}, fmt.Errorf("move checked-out tree into place: %w", err)
	}

	if err := hasher.WriteExecBits(dst, execBits); err != nil {
		return time.Time{}, fmt.Errorf("write exec-bits metadata: %w", err)
	}

	return commitTime, nil
}

// rejectLFS fails with exit 2 when any .gitattributes in the tree declares
// the lfs filter: a plain git checkout would pin pointer files instead of
// content (spec §5.5).
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
