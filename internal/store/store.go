// Copyright 2026 The Graft Authors

// Package store is graft's content-addressed store (spec §5.4): an immutable
// file tree per lockfile content hash, at store/sha256/<xx>/<rest>. Because the
// key is the hash graft.lock already records, a store hit needs no network, and
// identical content — even from different repos or versions — is stored once.
// Entries are created by an atomic same-filesystem rename and made read-only;
// a copy-mode install still re-hashes what it materializes (spec §5.4), so a
// corrupted entry is never installed. Deleting the store is always safe.
package store

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/gitrun"
	"github.com/min0625/graft/internal/hasher"
)

// Path returns the store entry path for a lockfile content hash, which has the
// form "sha256:<64 hex>": <storeRoot>/sha256/<xx>/<rest>, sharded by the first
// byte of the hex digest.
func Path(storeRoot, hash string) string {
	hex := strings.TrimPrefix(hash, "sha256:")

	return filepath.Join(storeRoot, "sha256", hex[:2], hex[2:])
}

// Exists reports whether the store holds an entry for hash.
func Exists(storeRoot, hash string) bool {
	info, err := os.Stat(Path(storeRoot, hash))

	return err == nil && info.IsDir()
}

// Insert publishes the verified tree at src as the store entry for hash and
// returns its path. src must already hash to hash and sit on the same
// filesystem as storeRoot (both under the cache), so the move is an atomic
// rename. The entry is made read-only before it is published. If another
// process created the same entry first, src is discarded and the existing
// entry is used (spec §5.1 rename race).
func Insert(storeRoot, src, hash string) (string, error) {
	dst := Path(storeRoot, hash)

	if Exists(storeRoot, hash) {
		return dst, discard(src)
	}

	//nolint:gosec // The cache is world-readable by design.
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("create store shard: %w", err)
	}

	// Rename first, then make read-only: moving a directory needs write access
	// to it (to update ".."), so it cannot be stripped beforehand.
	if err := os.Rename(src, dst); err != nil {
		// A concurrent creator may have won the race between Exists and here.
		if Exists(storeRoot, hash) {
			return dst, discard(src)
		}

		return "", fmt.Errorf("publish store entry: %w", err)
	}

	if err := makeReadOnly(dst); err != nil {
		return "", err
	}

	return dst, nil
}

// Materialize copies the store entry at storePath into dst, which must not
// exist yet, using copy-on-write reflinks where the filesystem supports them
// (spec §5.4) and plain copies otherwise. dst files are writable — copy mode
// behaves exactly like graft without a cache, including the commit-vendor
// workflow.
func Materialize(storePath, dst string) error {
	return filepath.WalkDir(storePath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(storePath, p)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755) //nolint:gosec // Vendor trees are world-readable by design.
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		return copyOrReflink(p, target, writableMode(info.Mode()))
	})
}

// writableMode maps a (read-only) store file mode to the writable mode its
// copy in the vendor tree should get, preserving only the executable bit.
func writableMode(mode fs.FileMode) fs.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}

	return 0o644
}

// copyOrReflink reflinks src to dst when the filesystem supports it, falling
// back to a plain byte copy.
func copyOrReflink(src, dst string, mode fs.FileMode) error {
	if err := reflink(src, dst, mode); err == nil {
		return nil
	}

	return copyFile(src, dst, mode)
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // The path comes from the store tree.
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // Read-only file.

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) //nolint:gosec // Same as above.
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close() //nolint:errcheck,gosec // The copy error wins.

		return err
	}

	return out.Close()
}

// makeReadOnly strips write permission from every file in tree, so a published
// store entry cannot be mutated in place — including through a link-mode dest,
// where an accidental edit then fails immediately (spec §5.4). Directories stay
// writable so the entry can still be traversed and, by `cache prune`, removed.
func makeReadOnly(tree string) error {
	return filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		perm := fs.FileMode(0o444)
		if info.Mode()&0o111 != 0 {
			perm = 0o555
		}

		return os.Chmod(p, perm) //nolint:gosec // p is a store entry graft itself just created under the cache.
	})
}

// discard removes a tree that lost the insert race, clearing read-only bits
// first like gitrun.RemoveAll.
func discard(tree string) error {
	return gitrun.RemoveAll(tree)
}

// Verify re-hashes every store entry and deletes any whose content no longer
// matches its key, returning how many entries it checked and the deleted
// entries' hashes in sorted order (spec §4.7). A missing store reports zero.
func Verify(storeRoot string) (checked int, corrupted []string, err error) {
	err = eachEntry(storeRoot, func(hash, path string) error {
		checked++

		got, hashErr := hasher.HashTree(path)
		if hashErr == nil && got == hash {
			return nil
		}

		if err := gitrun.RemoveAll(path); err != nil {
			return fmt.Errorf("remove corrupted entry %s: %w", hash, err)
		}

		corrupted = append(corrupted, hash)

		return nil
	})
	if err != nil {
		return 0, nil, err
	}

	sort.Strings(corrupted)

	return checked, corrupted, nil
}

// Clean removes store entries that are neither referenced nor recent: a hash
// not in keep AND last modified before `before`. It returns the removed hashes
// in sorted order plus their total logical size (spec §4.7). Pass the hashes
// still referenced by registered link-mode dests; copy-mode vendors reference
// nothing, so only the age floor protects them.
//
// The age floor (entries newer than `before` survive) does two things: it keeps
// `apply` from racing a concurrent prune — an entry inserted but not yet linked
// is recent, so it is never reclaimed out from under the linking step — and it
// gives copy-mode entries a retention window instead of vanishing on the next
// prune. An expired entry costs only a re-fetch.
// ponytail: age = insert/rename mtime, not last use — a copy-mode entry used
// daily still expires `before`-old after insert. Touch entries on a store hit
// if that churn ever bites.
func Clean(storeRoot string, keep map[string]bool, before time.Time) (removed []string, freed int64, err error) {
	err = eachEntry(storeRoot, func(hash, path string) error {
		if keep[hash] {
			return nil
		}

		info, statErr := os.Stat(path)
		if statErr != nil {
			return fmt.Errorf("stat store entry %s: %w", hash, statErr)
		}

		if !info.ModTime().Before(before) {
			return nil
		}

		size, sizeErr := cachedir.DirSize(path)
		if sizeErr != nil {
			return sizeErr
		}

		if rmErr := gitrun.RemoveAll(path); rmErr != nil {
			return fmt.Errorf("remove store entry %s: %w", hash, rmErr)
		}

		removed = append(removed, hash)
		freed += size

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	sort.Strings(removed)

	return removed, freed, nil
}

// eachEntry calls fn for every store entry, passing its "sha256:<hex>" hash and
// absolute path. Entries live two levels deep: sha256/<xx>/<rest>.
func eachEntry(storeRoot string, fn func(hash, path string) error) error {
	algoDir := filepath.Join(storeRoot, "sha256")

	shards, err := os.ReadDir(algoDir)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("read store: %w", err)
	}

	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}

		rests, err := os.ReadDir(filepath.Join(algoDir, shard.Name()))
		if err != nil {
			return fmt.Errorf("read store shard: %w", err)
		}

		for _, rest := range rests {
			if !rest.IsDir() {
				continue
			}

			hash := "sha256:" + shard.Name() + rest.Name()
			if err := fn(hash, filepath.Join(algoDir, shard.Name(), rest.Name())); err != nil {
				return err
			}
		}
	}

	return nil
}
