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
)

// Fetch checks out the tree of commit from repo into dst, which must not
// exist yet; its parent directory is used for staging, so dst appears
// atomically. When path is non-empty only that subdirectory of the repo
// becomes dst. The returned time is the committer timestamp of commit.
//
// name is the dependency name, used in error messages. The checkout forces
// core.autocrlf=false and core.eol=lf and deletes .git, so identical bytes
// land on every platform (spec §3.2). Trees that use Git LFS are rejected
// with exit 2 (unsupported in v1).
func Fetch(ctx context.Context, name, repo, commit, path, dst string) (time.Time, error) {
	scratch, err := os.MkdirTemp(filepath.Dir(dst), ".graft-checkout-*")
	if err != nil {
		return time.Time{}, fmt.Errorf("create checkout staging dir: %w", err)
	}
	defer gitrun.RemoveAll(scratch) //nolint:errcheck // Best-effort staging cleanup.

	if err := checkout(ctx, name, repo, commit, scratch); err != nil {
		return time.Time{}, err
	}

	commitTime, err := gitrun.CommitTime(ctx, scratch, commit)
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

	if err := gitrun.RemoveAll(filepath.Join(scratch, ".git")); err != nil {
		return time.Time{}, fmt.Errorf("remove .git: %w", err)
	}

	if err := os.Rename(treeRoot, dst); err != nil {
		return time.Time{}, fmt.Errorf("move checked-out tree into place: %w", err)
	}

	return commitTime, nil
}

// checkout makes commit's tree appear in dir: a cheap depth-1 SHA fetch
// first, then the full-fetch fallback (spec §5.5), distinguishing a network
// failure (exit 3) from a commit that no longer exists on the remote
// (exit 1, with a re-pin hint).
func checkout(ctx context.Context, name, repo, commit, dir string) error {
	if _, err := gitrun.Run(ctx, "", "init", "--quiet", dir); err != nil {
		return fmt.Errorf("git init: %w", err)
	}

	for _, kv := range [][2]string{{"core.autocrlf", "false"}, {"core.eol", "lf"}} {
		if _, err := gitrun.Run(ctx, dir, "config", kv[0], kv[1]); err != nil {
			return fmt.Errorf("git config: %w", err)
		}
	}

	if gitrun.FetchSHA(ctx, dir, repo, commit) != nil {
		// The server rejected the SHA fetch (or the fetch failed): fall back
		// to a full fetch, which FetchAll classifies as exit 3 when the
		// remote is unreachable.
		if err := gitrun.FetchAll(ctx, dir, repo); err != nil {
			return err
		}

		if _, err := gitrun.Run(ctx, dir, "rev-parse", "--verify", "--quiet", commit+"^{commit}"); err != nil {
			return clierr.New(clierr.CodeGeneral,
				fmt.Sprintf("commit %.12s for dependency %q not found in %s", commit, name, repo),
				"the locked commit no longer exists on the remote — its history may have been rewritten",
				fmt.Sprintf("run `graft add %s@<ref>` to re-pin the dependency, then commit the updated files", name),
			)
		}
	}

	if _, err := gitrun.Run(ctx, dir,
		"-c", "advice.detachedHead=false", "checkout", "--quiet", commit); err != nil {
		return fmt.Errorf("git checkout %.12s: %w", commit, err)
	}

	return nil
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
