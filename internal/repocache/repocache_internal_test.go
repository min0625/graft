// Copyright 2026 The Graft Authors

package repocache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gittest"
)

// TestFetchRef_rejectsOptionLikeRef guards fetchRef against option
// injection: the middle fallback step of fetchCommit (spec §5.5) passes the
// manifest/lockfile "version" string to fetchRef verbatim, and that string
// is not validated against git's flag syntax. A ref shaped like a git option
// (e.g. "--upload-pack=...") must be rejected as a literal, unmatched ref —
// never parsed by git as a flag.
func TestFetchRef_rejectsOptionLikeRef(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	r := gittest.New(t)
	bare := BarePath(cache, r.URL())

	if err := ensureBare(t.Context(), bare, r.URL()); err != nil {
		t.Fatalf("ensureBare: %v", err)
	}

	canary := filepath.Join(t.TempDir(), "canary")

	if err := fetchRef(t.Context(), bare, "--upload-pack=touch "+canary, false); err == nil {
		t.Fatal("fetchRef succeeded on an option-like ref, want an error")
	}

	if _, err := os.Stat(canary); err == nil {
		t.Fatal("ref was parsed as a git option instead of a literal ref")
	}
}

// TestIsTag checks that an empty version and a pseudo-version are excluded,
// while any other non-empty string counts as a tag (spec §5.5's middle
// fallback step only applies to real tags).
func TestIsTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version string
		want    bool
	}{
		{"", false},
		{"v1.0.0", true},
		{"v0.0.0-20240101000000-abcdef012345", false}, // pseudo-version
		{"main", true},
	}

	for _, tt := range tests {
		if got := isTag(tt.version); got != tt.want {
			t.Errorf("isTag(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

// TestPseudoVersion checks the v0.0.0-<14-digit timestamp>-<12-hex-sha> shape
// is recognized, and that real tags and malformed near-misses are not.
func TestPseudoVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version string
		want    bool
	}{
		{"v0.0.0-20240101000000-abcdef012345", true},
		{"v1.0.0", false},
		{"", false},
		{"v0.0.0-2024-abcdef012345", false},          // timestamp too short
		{"v0.0.0-20240101000000-abcdef01234", false}, // sha too short
		{"v0.0.0-20240101000000", false},             // missing sha segment
	}

	for _, tt := range tests {
		if got := pseudoVersion(tt.version); got != tt.want {
			t.Errorf("pseudoVersion(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

// TestCommitGoneErr checks the exit-1 error (spec §6) names the repo and a
// truncated commit SHA.
func TestCommitGoneErr(t *testing.T) {
	t.Parallel()

	err := commitGoneErr("github.com/org/repo", "abcdef0123456789abcdef0123456789abcdef01")

	if clierr.ExitCode(err) != int(clierr.CodeGeneral) {
		t.Errorf("ExitCode = %d, want %d", clierr.ExitCode(err), clierr.CodeGeneral)
	}

	if !strings.Contains(err.Error(), "github.com/org/repo") {
		t.Errorf("error %q does not mention the repo", err.Error())
	}

	if !strings.Contains(err.Error(), "abcdef012345") {
		t.Errorf("error %q does not mention the truncated commit", err.Error())
	}
}

// TestFetchAllRefs verifies the always-correct fallback (spec §5.5 step 3)
// fetches every branch and tag from origin into the bare repo.
func TestFetchAllRefs(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	r.Commit("first")
	r.Tag("v1.0.0")
	r.Branch("feature")

	bare := BarePath(cache, r.URL())
	if err := ensureBare(t.Context(), bare, r.URL()); err != nil {
		t.Fatalf("ensureBare: %v", err)
	}

	if err := fetchAllRefs(t.Context(), bare, false); err != nil {
		t.Fatalf("fetchAllRefs: %v", err)
	}

	if _, err := bareGit(
		t.Context(),
		bare,
		"rev-parse",
		"--verify",
		"--quiet",
		"refs/remotes/origin/feature",
	); err != nil {
		t.Error("fetchAllRefs did not fetch the feature branch")
	}

	if _, err := bareGit(t.Context(), bare, "rev-parse", "--verify", "--quiet", "refs/tags/v1.0.0"); err != nil {
		t.Error("fetchAllRefs did not fetch the v1.0.0 tag")
	}
}

// TestFetchAll_unreachable verifies that fetchAll classifies a failure
// against an unreachable remote as the spec §4.5 exit-3 network error.
func TestFetchAll_unreachable(t *testing.T) {
	t.Parallel()

	cache := t.TempDir()
	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	r.Commit("first")

	bare := BarePath(cache, r.URL())
	if err := ensureBare(t.Context(), bare, r.URL()); err != nil {
		t.Fatalf("ensureBare: %v", err)
	}

	// Remove the remote so origin is no longer reachable.
	if err := os.RemoveAll(r.Dir); err != nil {
		t.Fatal(err)
	}

	err := fetchAll(t.Context(), bare, r.URL(), false)
	if err == nil {
		t.Fatal("fetchAll succeeded against a removed remote, want an error")
	}

	if clierr.ExitCode(err) != int(clierr.CodeNetwork) {
		t.Errorf("ExitCode = %d, want %d (network)", clierr.ExitCode(err), clierr.CodeNetwork)
	}
}
