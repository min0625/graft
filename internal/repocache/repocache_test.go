// Copyright 2026 The Graft Authors

package repocache_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/min0625/graft/internal/gittest"
	"github.com/min0625/graft/internal/repocache"
)

// TestBarePath_canonicalKey checks that every spelling of one remote — HTTPS,
// scheme-less, .git-suffixed, and scp-like SSH — maps to the same bare-repo
// path, so they all share one cache entry (spec §5.6, §10.8).
func TestBarePath_canonicalKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	want := repocache.BarePath(root, "github.com/org/repo")

	for _, spelling := range []string{
		"github.com/org/repo",
		"github.com/org/repo.git",
		"https://github.com/org/repo",
		"https://github.com/org/repo.git",
		"git@github.com:org/repo.git",
		"ssh://git@github.com/org/repo.git",
	} {
		if got := repocache.BarePath(root, spelling); got != want {
			t.Errorf("BarePath(%q) = %q, want %q", spelling, got, want)
		}
	}
}

// TestEnsureCommit_incrementalOffline verifies that a commit already in the
// cache is never re-fetched: after the first fetch the remote is removed, and
// a second EnsureCommit of the same commit still succeeds (spec §5.5).
func TestEnsureCommit_incrementalOffline(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	sha := r.Commit("first")

	if _, err := repocache.EnsureCommit(t.Context(), cache, r.URL(), sha, "", ""); err != nil {
		t.Fatalf("first EnsureCommit: %v", err)
	}

	// Make the remote unreachable; the cached commit must still resolve.
	if err := os.RemoveAll(r.Dir); err != nil {
		t.Fatal(err)
	}

	bare, err := repocache.EnsureCommit(t.Context(), cache, r.URL(), sha, "", "")
	if err != nil {
		t.Fatalf("second EnsureCommit must be offline: %v", err)
	}

	if want := repocache.BarePath(cache, r.URL()); bare != want {
		t.Errorf("bare = %q, want %q", bare, want)
	}
}

// TestEnsureCommit_tagFallback covers the middle fallback step: a non-tip
// commit whose SHA fetch a file:// remote rejects, fetched by its tag.
func TestEnsureCommit_tagFallback(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	r := gittest.New(t)
	r.WriteFile("a.txt", "old\n")
	old := r.Commit("first")
	r.Tag("v1.0.0")
	r.WriteFile("a.txt", "new\n")
	r.Commit("second")

	bare, err := repocache.EnsureCommit(t.Context(), cache, r.URL(), old, "v1.0.0", "")
	if err != nil {
		t.Fatalf("EnsureCommit via tag: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "tree")
	if err := repocache.Checkout(t.Context(), bare, old, "", dst); err != nil {
		t.Fatalf("Checkout: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "a.txt")) //nolint:gosec // Test-controlled path.
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "old\n" {
		t.Errorf("a.txt = %q, want the tagged commit's content", data)
	}
}

// TestEnsureCommit_reusesBareAcrossCommits checks that a second commit from
// the same repo lands in the same bare repository (one entry per repo).
func TestEnsureCommit_reusesBareAcrossCommits(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	r := gittest.New(t)
	r.WriteFile("a.txt", "1\n")
	first := r.Commit("first")
	r.WriteFile("a.txt", "2\n")
	second := r.Commit("second")

	b1, err := repocache.EnsureCommit(t.Context(), cache, r.URL(), first, "", "")
	if err != nil {
		t.Fatal(err)
	}

	b2, err := repocache.EnsureCommit(t.Context(), cache, r.URL(), second, "", "")
	if err != nil {
		t.Fatal(err)
	}

	if b1 != b2 {
		t.Errorf("two commits of one repo used different bare repos: %q vs %q", b1, b2)
	}
}
