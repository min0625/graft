// Copyright 2026 The Graft Authors

// Package links is the registry of link-mode vendor dests (spec §5.6): one
// small file per dest under <cache>/links, recording which store entry the
// dest's symlink points at. `graft cache clean` reads it to know which store
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

// ReferencedHashes returns the set of store hashes any registered link-mode
// dest still points at, so clean can keep exactly those entries. A missing
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

		hash, err := firstLine(filepath.Join(linksDir, e.Name()))
		if err != nil {
			return nil, err
		}

		if hash != "" {
			referenced[hash] = true
		}
	}

	return referenced, nil
}

// recordPath maps a dest to its registration file, keyed by the hash of the
// dest's absolute path so the registry never stores repo-relative ambiguity.
func recordPath(linksDir, destAbs string) string {
	sum := sha256.Sum256([]byte(destAbs))

	return filepath.Join(linksDir, hex.EncodeToString(sum[:]))
}

func firstLine(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // The path comes from reading the cache.
	if err != nil {
		return "", fmt.Errorf("read link registration: %w", err)
	}
	defer f.Close() //nolint:errcheck // Read-only file.

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), scanner.Err()
	}

	return "", scanner.Err()
}
