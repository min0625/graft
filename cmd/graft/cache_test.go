// Copyright 2026 The Graft Authors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/clierr"
)

// lockedProject sets up an isolated project with one locked dep, so the
// content store and a bare repo are populated, and returns the cache dir.
func lockedProject(t *testing.T) string {
	t.Helper()

	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	cache, err := cachedir.Dir()
	if err != nil {
		t.Fatal(err)
	}

	return cache
}

// storeEntries lists the store entry directories under cache.
func storeEntries(t *testing.T, cache string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(cache, "store", "sha256", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}

	return matches
}

func TestCache_dir(t *testing.T) {
	newProjectDir(t)

	cache, err := cachedir.Dir()
	if err != nil {
		t.Fatal(err)
	}

	out := mustRunGraft(t, "cache", "dir")

	if want := cache + "\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestCache_verifyIntact(t *testing.T) {
	lockedProject(t)

	out := mustRunGraft(t, "cache", "verify")

	if want := "✓ verified 1 store entry\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestCache_verifyDeletesCorrupted(t *testing.T) {
	cache := lockedProject(t)

	entries := storeEntries(t, cache)
	if len(entries) != 1 {
		t.Fatalf("want 1 store entry, got %d", len(entries))
	}

	// Tamper with a store file (read-only — make it writable first).
	victim := filepath.Join(entries[0], "run.sh")
	if err := os.Chmod(victim, 0o644); err != nil { //nolint:gosec // Test fixture.
		t.Fatal(err)
	}

	if err := os.WriteFile(victim, []byte("tampered\n"), 0o644); err != nil { //nolint:gosec // Test fixture.
		t.Fatal(err)
	}

	out, err := runGraft(t, "cache", "verify")
	wantExit(t, err, clierr.CodeIntegrity)

	if !strings.Contains(out, "✗ removed corrupted entry") {
		t.Errorf("output = %q", out)
	}

	if remaining := storeEntries(t, cache); len(remaining) != 0 {
		t.Errorf("corrupted entry was not removed: %v", remaining)
	}
}

func TestCache_cleanRemovesUnreferencedStore(t *testing.T) {
	cache := lockedProject(t)

	if len(storeEntries(t, cache)) != 1 {
		t.Fatal("expected one store entry after lock")
	}

	out := mustRunGraft(t, "cache", "clean")

	if !strings.Contains(out, "removed 1 store entry") {
		t.Errorf("output = %q", out)
	}

	if remaining := storeEntries(t, cache); len(remaining) != 0 {
		t.Errorf("copy-mode store entry should be unreferenced and removed: %v", remaining)
	}
}

// TestCache_cleanReclaimsAfterLinkRewrite covers spec §5.6: a store entry kept
// alive by a link-mode dest must become reclaimable once that dest is rewritten
// to a copy, so the now-stale link registration no longer pins it.
func TestCache_cleanReclaimsAfterLinkRewrite(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	cache, err := cachedir.Dir()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("GRAFT_LINK_MODE", "symlink")
	mustRunGraft(t, "apply")

	// While the link is live the entry is referenced and clean keeps it.
	mustRunGraft(t, "cache", "clean")

	if len(storeEntries(t, cache)) != 1 {
		t.Fatal("clean removed a store entry a live link still references")
	}

	// Rewrite the dest to a copy; the link registration is now stale.
	t.Setenv("GRAFT_LINK_MODE", "copy")
	mustRunGraft(t, "apply")

	mustRunGraft(t, "cache", "clean")

	if remaining := storeEntries(t, cache); len(remaining) != 0 {
		t.Errorf("stale link registration kept a store entry alive: %v", remaining)
	}
}

func TestCache_cleanNoop(t *testing.T) {
	newProjectDir(t)

	out := mustRunGraft(t, "cache", "clean")

	if want := "✓ cache already clean\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestCache_cleanAll(t *testing.T) {
	cache := lockedProject(t)

	out := mustRunGraft(t, "cache", "clean", "--all")

	if want := "✓ removed the entire cache\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}

	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Error("cache directory survived clean --all")
	}
}
