// Copyright 2026 The Graft Authors

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/config"
	"github.com/min0625/graft/internal/fetcher"
	"github.com/min0625/graft/internal/gitrun"
	"github.com/min0625/graft/internal/hasher"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/min0625/graft/internal/resolver"
	"github.com/min0625/graft/internal/store"
)

// relock computes a fresh lockfile for the manifest, with the `graft lock`
// semantics of spec §4.1: entries whose repo and version are unchanged keep
// their locked commit without any network access; new entries and entries
// whose repo or version changed are re-resolved and fetched to compute their
// content hash; a changed path alone re-fetches the already-locked commit
// (no ref lookup). Nothing is installed.
//
// hints carries resolutions the caller (graft add) already performed, keyed
// by dep name, so the touched dep is not resolved twice.
func relock(
	ctx context.Context,
	m *config.Manifest,
	prev *lockfile.Lockfile,
	hints map[string]resolver.Resolution,
) (*lockfile.Lockfile, error) {
	next := lockfile.New()
	next.Vendor = m.Vendor
	next.Deps = make([]lockfile.LockedDep, 0, len(m.Deps))

	for _, dep := range m.Deps {
		entry, err := relockDep(ctx, prev, dep, hints)
		if err != nil {
			return nil, err
		}

		next.Deps = append(next.Deps, entry)
	}

	return next, nil
}

func relockDep(
	ctx context.Context,
	prev *lockfile.Lockfile,
	dep config.Dep,
	hints map[string]resolver.Resolution,
) (lockfile.LockedDep, error) {
	_, explicitlyTouched := hints[dep.Name]

	if !explicitlyTouched {
		prevDep := prev.FindDep(dep.Name)
		if prevDep != nil && prevDep.Repo == dep.Repo && prevDep.Version == dep.Version {
			return relockFromPrev(ctx, dep, prevDep)
		}
	}

	res, ok := hints[dep.Name]
	if !ok {
		var err error

		res, err = resolver.ResolveVersion(ctx, dep.Repo, dep.Version)
		if err != nil {
			return lockfile.LockedDep{}, err
		}
	}

	hash, err := fetchHash(ctx, dep, res.Commit)
	if err != nil {
		return lockfile.LockedDep{}, err
	}

	return newEntry(dep, res.Commit, res.Time, hash), nil
}

// relockFromPrev reuses or partially reuses a locked entry whose repo and
// version are unchanged. An identical path reuses the entry verbatim (spec §7);
// a changed path re-fetches the locked commit to recompute the hash.
func relockFromPrev(
	ctx context.Context, dep config.Dep, prevDep *lockfile.LockedDep,
) (lockfile.LockedDep, error) {
	if prevDep.Path == dep.Path {
		return *prevDep, nil
	}

	hash, err := fetchHash(ctx, dep, prevDep.Commit)
	if err != nil {
		return lockfile.LockedDep{}, err
	}

	return newEntry(dep, prevDep.Commit, prevDep.Time, hash), nil
}

func newEntry(dep config.Dep, commit string, commitTime time.Time, hash string) lockfile.LockedDep {
	return lockfile.LockedDep{
		Name:    dep.Name,
		Repo:    dep.Repo,
		Version: dep.Version,
		Path:    dep.Path,
		Commit:  commit,
		Time:    commitTime.UTC(),
		Hash:    hash,
	}
}

// fetchHash fetches dep's tree at commit, returns its content hash, and
// pre-populates the content store with it — so a later `graft apply` of the
// same lockfile installs without any download (spec §5.6).
func fetchHash(ctx context.Context, dep config.Dep, commit string) (string, error) {
	cacheRoot, err := cachedir.Dir()
	if err != nil {
		return "", err
	}

	storeRoot, err := cachedir.Store()
	if err != nil {
		return "", err
	}

	tmpDir, err := cachedir.Tmp()
	if err != nil {
		return "", err
	}

	holder, err := os.MkdirTemp(tmpDir, "checkout-*")
	if err != nil {
		return "", fmt.Errorf("create checkout staging dir: %w", err)
	}
	defer gitrun.RemoveAll(holder) //nolint:errcheck // Best-effort temp cleanup.

	dst := filepath.Join(holder, "tree")

	if _, err := fetcher.Fetch(ctx, cacheRoot, dep.Name, dep.Repo, commit, dep.Version, dep.Path, dst); err != nil {
		return "", err
	}

	hash, err := hasher.HashTree(dst)
	if err != nil {
		return "", err
	}

	if _, err := store.Insert(storeRoot, dst, hash); err != nil {
		return "", err
	}

	return hash, nil
}
