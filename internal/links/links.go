// Copyright 2026 The Graft Authors

// Package links is the registry of link-mode vendor dests (spec §5.4): one
// small file per dest under <cache>/links, recording which store entry the
// dest's symlink points at. `graft cache prune` reads it to know which store
// entries are still referenced; link-mode `graft apply` writes it. The
// registry is purely advisory — a stale entry only keeps a store entry alive
// until the next clean, and deleting the cache is always safe.
package links

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Register records that the link-mode dest at destAbs points at the store
// entry for hash, creating linksDir if needed. Re-registering the same dest
// overwrites the previous record.
func Register(linksDir, destAbs, hash string) error {
	//nolint:gosec // The cache is world-readable by design.
	if err := os.MkdirAll(linksDir, 0o755); err != nil {
		return fmt.Errorf("create links directory: %w", err)
	}

	// hash on the first line (read by clean), dest on the second for humans.
	content := hash + "\n" + destAbs + "\n"

	if err := os.WriteFile(recordPath(linksDir, destAbs), []byte(content), 0o600); err != nil {
		return fmt.Errorf("write link registration: %w", err)
	}

	return nil
}

// ReferencedHashes returns the set of store hashes that a still-live link-mode
// dest points at, so clean can keep exactly those entries. A registration
// whose dest no longer links to its recorded hash — the dest was removed, or
// rewritten to a copy-mode tree — is stale: it is pruned and does not keep its
// store entry alive, so clean can actually reclaim it (spec §5.4). A missing
// links directory yields an empty set.
func ReferencedHashes(linksDir string) (map[string]bool, error) {
	entries, err := os.ReadDir(linksDir)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read links directory: %w", err)
	}

	referenced := make(map[string]bool, len(entries))

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		record := filepath.Join(linksDir, e.Name())

		hash, dest, err := readRecord(record)
		if err != nil {
			return nil, err
		}

		if hash != "" && linkLive(dest, hash) {
			referenced[hash] = true

			continue
		}

		// Prune the stale registration; the cache is purely advisory, so
		// dropping it is always safe.
		if err := os.Remove(record); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("prune stale link registration: %w", err)
		}
	}

	return referenced, nil
}

// linkLive reports whether destAbs is still a symlink (or Windows junction)
// pointing at the store entry for hash. A missing or rewritten dest makes the
// registration stale.
func linkLive(destAbs, hash string) bool {
	target, err := os.Readlink(destAbs)
	if err != nil {
		return false
	}

	target = filepath.Clean(strings.TrimPrefix(target, `\\?\`))

	hex := strings.TrimPrefix(hash, "sha256:")
	if len(hex) < 2 {
		return false
	}

	// The store lays an entry out as sha256/<xx>/<rest>; the link target is the
	// absolute path of that entry. Matching the suffix keeps links from having
	// to import the store package.
	suffix := filepath.Join("sha256", hex[:2], hex[2:])

	return strings.HasSuffix(target, suffix)
}

// recordPath maps a dest to its registration file, keyed by the hash of the
// dest's absolute path so the registry never stores repo-relative ambiguity.
func recordPath(linksDir, destAbs string) string {
	sum := sha256.Sum256([]byte(destAbs))

	return filepath.Join(linksDir, hex.EncodeToString(sum[:]))
}

// readRecord reads a registration's hash (first line) and dest (second line),
// the inverse of the layout Register writes.
func readRecord(path string) (hash, dest string, err error) {
	f, err := os.Open(path) //nolint:gosec // The path comes from reading the cache.
	if err != nil {
		return "", "", fmt.Errorf("read link registration: %w", err)
	}
	defer f.Close() //nolint:errcheck // Read-only file.

	scanner := bufio.NewScanner(f)

	if scanner.Scan() {
		hash = strings.TrimSpace(scanner.Text())
	}

	if scanner.Scan() {
		dest = strings.TrimSpace(scanner.Text())
	}

	return hash, dest, scanner.Err()
}
