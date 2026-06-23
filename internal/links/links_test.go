// Copyright 2026 The Graft Authors

package links_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/min0625/graft/internal/links"
)

const testHash = "sha256:c7d4b6acdb44a552610c48cbffbffcef2a4d1165dcc081927d1c1875fb63a1ee"

func TestRegister_recordExists(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	linksDir := filepath.Join(tmp, "links")
	dest := filepath.Join(tmp, "vendor", "dep")

	if err := links.Register(linksDir, dest, testHash); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(linksDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("want 1 record, got %d", len(entries))
	}
}

func TestRegister_overwrite(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	linksDir := filepath.Join(tmp, "links")
	dest := filepath.Join(tmp, "vendor", "dep")

	// Second call on the same dest must succeed and not leave a second record.
	for range 2 {
		if err := links.Register(linksDir, dest, testHash); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(linksDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("want 1 record after two calls, got %d", len(entries))
	}
}

func TestReferencedHashes_emptyDir(t *testing.T) {
	t.Parallel()

	refs, err := links.ReferencedHashes(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) != 0 {
		t.Fatalf("want empty map for nonexistent dir, got %v", refs)
	}
}

func TestReferencedHashes_liveLink(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	linksDir := filepath.Join(tmp, "links")

	// Build a fake store entry that linkLive will recognise.
	// linkLive checks strings.HasSuffix(target, "sha256/<xx>/<rest>").
	hex := "c7d4b6acdb44a552610c48cbffbffcef2a4d1165dcc081927d1c1875fb63a1ee"
	storeEntry := filepath.Join(tmp, "store", "sha256", hex[:2], hex[2:])

	if err := os.MkdirAll(storeEntry, 0o750); err != nil {
		t.Fatal(err)
	}

	// A symlink pointing at the store entry is considered "live".
	dest := filepath.Join(tmp, "vendor", "dep")

	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(storeEntry, dest); err != nil {
		t.Fatal(err)
	}

	if err := links.Register(linksDir, dest, testHash); err != nil {
		t.Fatal(err)
	}

	refs, err := links.ReferencedHashes(linksDir)
	if err != nil {
		t.Fatal(err)
	}

	if !refs[testHash] {
		t.Errorf("hash %q not in referenced set %v", testHash, refs)
	}
}

func TestReferencedHashes_staleEntryPruned(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	linksDir := filepath.Join(tmp, "links")

	// Register a dest that does not exist as a symlink → stale.
	dest := filepath.Join(tmp, "vendor", "dep")

	if err := links.Register(linksDir, dest, testHash); err != nil {
		t.Fatal(err)
	}

	refs, err := links.ReferencedHashes(linksDir)
	if err != nil {
		t.Fatal(err)
	}

	if refs[testHash] {
		t.Error("stale link should not appear in referenced set")
	}

	// The stale record must have been pruned.
	entries, err := os.ReadDir(linksDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Errorf("want 0 records after stale prune, got %d", len(entries))
	}
}

func TestReferencedHashes_dirEntriesSkipped(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	linksDir := filepath.Join(tmp, "links")

	if err := os.MkdirAll(linksDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// A sub-directory inside linksDir should not cause an error.
	if err := os.Mkdir(filepath.Join(linksDir, "subdir"), 0o750); err != nil {
		t.Fatal(err)
	}

	refs, err := links.ReferencedHashes(linksDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(refs) != 0 {
		t.Fatalf("want empty map when only sub-dir present, got %v", refs)
	}
}
