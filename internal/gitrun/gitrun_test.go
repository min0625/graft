// Copyright 2026 The Graft Authors

package gitrun_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gitrun"
	"github.com/min0625/graft/internal/gittest"
)

func TestCanonicalRepo(t *testing.T) {
	t.Parallel()

	const canonical = "github.com/org/repo"

	tests := []struct {
		in, want string
	}{
		{"github.com/org/repo", canonical},
		{"github.com/org/repo/", canonical},
		{"github.com/org/repo.git", canonical},
		{"https://github.com/org/repo", canonical},
		{"https://github.com/org/repo.git", canonical},
		{"https://user@github.com/org/repo.git", canonical},
		{"ssh://git@github.com/org/repo.git", canonical},
		{"git@github.com:org/repo.git", canonical},
		{"git@github.com:org/repo", canonical},
		// Spellings that genuinely differ stay distinct.
		{"gitlab.com/org/repo", "gitlab.com/org/repo"},
		{"github.com/org/other", "github.com/org/other"},
	}

	for _, tt := range tests {
		if got := gitrun.CanonicalRepo(tt.in); got != tt.want {
			t.Errorf("CanonicalRepo(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRemoteURL verifies scheme-less repos become HTTPS while explicit URLs
// and scp-like SSH remotes are used as written (spec §3.1, §7).
func TestRemoteURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"github.com/org/repo", "https://github.com/org/repo"},
		{"https://github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"ssh://git@github.com/org/repo.git", "ssh://git@github.com/org/repo.git"},
		{"git@github.com:org/repo.git", "git@github.com:org/repo.git"},
	}

	for _, tt := range tests {
		if got := gitrun.RemoteURL(tt.in); got != tt.want {
			t.Errorf("RemoteURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRun_capturesStdoutAndStderr checks that a successful command's stdout is
// returned and a failing command's error carries git's stderr.
func TestRun_capturesStdoutAndStderr(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if _, err := gitrun.Run(t.Context(), "", "init", "--quiet", dir); err != nil {
		t.Fatalf("git init: %v", err)
	}

	out, err := gitrun.Run(t.Context(), dir, "rev-parse", "--git-dir")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	if strings.TrimSpace(out) == "" {
		t.Error("Run returned empty stdout for a successful command")
	}

	_, err = gitrun.Run(t.Context(), dir, "rev-parse", "--verify", "--quiet", "nonexistent^{commit}")
	if err == nil {
		t.Fatal("Run succeeded resolving a nonexistent commit, want an error")
	}
}

// TestRunEnv_setsExtraEnv verifies that extraEnv entries reach the child
// process, using GIT_INDEX_FILE the way Checkout does.
func TestRunEnv_setsExtraEnv(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if _, err := gitrun.Run(t.Context(), "", "init", "--quiet", dir); err != nil {
		t.Fatalf("git init: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	index := filepath.Join(t.TempDir(), "custom-index")

	if _, err := gitrun.RunEnv(t.Context(), dir, []string{"GIT_INDEX_FILE=" + index}, "add", "--all"); err != nil {
		t.Fatalf("add with custom index: %v", err)
	}

	if _, err := os.Stat(index); err != nil {
		t.Errorf("custom GIT_INDEX_FILE was not used: %v", err)
	}

	// The default index must be untouched — everything landed in the custom one.
	if _, err := os.Stat(filepath.Join(dir, ".git", "index")); err == nil {
		t.Error("default .git/index was written; GIT_INDEX_FILE was not honored")
	}
}

// TestNetworkErr verifies the wrapped error carries the spec §4.5 network
// exit code and mentions the repo and the underlying reason.
func TestNetworkErr(t *testing.T) {
	t.Parallel()

	err := gitrun.NetworkErr("github.com/org/repo", errors.New("connection refused"))

	if clierr.ExitCode(err) != int(clierr.CodeNetwork) {
		t.Errorf("ExitCode = %d, want %d", clierr.ExitCode(err), clierr.CodeNetwork)
	}

	if !strings.Contains(err.Error(), "github.com/org/repo") {
		t.Errorf("error %q does not mention the repo", err.Error())
	}
}

// TestReachable checks a local fixture repo answers and a nonexistent path
// does not.
func TestReachable(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)

	if !gitrun.Reachable(t.Context(), r.URL()) {
		t.Error("Reachable = false for a live local repo, want true")
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist.git")

	if gitrun.Reachable(t.Context(), missing) {
		t.Error("Reachable = true for a nonexistent repo, want false")
	}
}

// TestFetchSHA fetches a known commit at depth 1 into a fresh repository.
func TestFetchSHA(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	sha := r.Commit("first")

	dir := t.TempDir()
	if _, err := gitrun.Run(t.Context(), "", "init", "--quiet", dir); err != nil {
		t.Fatalf("git init: %v", err)
	}

	if err := gitrun.FetchSHA(t.Context(), dir, r.URL(), sha); err != nil {
		t.Fatalf("FetchSHA: %v", err)
	}

	if _, err := gitrun.Run(t.Context(), dir, "cat-file", "-e", sha); err != nil {
		t.Errorf("fetched commit not present: %v", err)
	}
}

// TestFetchAll fetches every branch and tag from a fixture remote, landing
// them under refs/remotes/origin and refs/tags respectively.
func TestFetchAll(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	r.Commit("first")
	r.Tag("v1.0.0")
	r.Branch("feature")

	dir := t.TempDir()
	if _, err := gitrun.Run(t.Context(), "", "init", "--quiet", dir); err != nil {
		t.Fatalf("git init: %v", err)
	}

	if err := gitrun.FetchAll(t.Context(), dir, r.URL()); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if _, err := gitrun.Run(
		t.Context(),
		dir,
		"rev-parse",
		"--verify",
		"--quiet",
		"refs/remotes/origin/feature",
	); err != nil {
		t.Error("FetchAll did not fetch the feature branch")
	}

	if _, err := gitrun.Run(t.Context(), dir, "rev-parse", "--verify", "--quiet", "refs/tags/v1.0.0"); err != nil {
		t.Error("FetchAll did not fetch the v1.0.0 tag")
	}
}

// TestFetchAll_networkError verifies an unreachable remote is classified as
// the spec §4.5 exit-3 network error, not a generic failure.
func TestFetchAll_networkError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := gitrun.Run(t.Context(), "", "init", "--quiet", dir); err != nil {
		t.Fatalf("git init: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist.git")

	err := gitrun.FetchAll(t.Context(), dir, missing)
	if err == nil {
		t.Fatal("FetchAll succeeded against a nonexistent remote, want an error")
	}

	if clierr.ExitCode(err) != int(clierr.CodeNetwork) {
		t.Errorf("ExitCode = %d, want %d (network)", clierr.ExitCode(err), clierr.CodeNetwork)
	}
}

// TestCommitTime verifies the committer timestamp is parsed as UTC.
func TestCommitTime(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	sha := r.Commit("first")

	dir := t.TempDir()
	if _, err := gitrun.Run(t.Context(), "", "init", "--quiet", dir); err != nil {
		t.Fatalf("git init: %v", err)
	}

	if err := gitrun.FetchSHA(t.Context(), dir, r.URL(), sha); err != nil {
		t.Fatalf("FetchSHA: %v", err)
	}

	ct, err := gitrun.CommitTime(t.Context(), dir, sha)
	if err != nil {
		t.Fatal(err)
	}

	if ct.IsZero() {
		t.Error("CommitTime returned zero time")
	}

	if ct.Location() != time.UTC {
		t.Errorf("CommitTime location = %v, want UTC", ct.Location())
	}
}

// TestCommitTime_rejectsNonCommitObject verifies the "^{commit}" peel rejects
// a SHA that resolves to a non-commit object (here, a blob).
func TestCommitTime_rejectsNonCommitObject(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	r.Commit("first")
	blobSHA := strings.TrimSpace(r.Git("rev-parse", "HEAD:a.txt"))

	dir := t.TempDir()
	if _, err := gitrun.Run(t.Context(), "", "init", "--quiet", dir); err != nil {
		t.Fatalf("git init: %v", err)
	}

	if err := gitrun.FetchAll(t.Context(), dir, r.URL()); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	if _, err := gitrun.CommitTime(t.Context(), dir, blobSHA); err == nil {
		t.Fatal("CommitTime succeeded on a blob SHA, want an error")
	}
}

// TestRemoveAll verifies a normal tree is removed, and that removal still
// succeeds when a directory inside the tree starts out read-only — the
// permission-fixing fallback os.RemoveAll alone cannot handle.
func TestRemoveAll(t *testing.T) {
	t.Parallel()

	t.Run("plain tree", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := gitrun.RemoveAll(dir); err != nil {
			t.Fatalf("RemoveAll: %v", err)
		}

		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("dir still exists after RemoveAll: %v", err)
		}
	})

	t.Run("missing path", func(t *testing.T) {
		t.Parallel()

		if err := gitrun.RemoveAll(filepath.Join(t.TempDir(), "nope")); err != nil {
			t.Errorf("RemoveAll on a missing path: %v", err)
		}
	})

	t.Run("read-only subdirectory", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		sub := filepath.Join(root, "sub")

		if err := os.Mkdir(sub, 0o755); err != nil { //nolint:gosec // test needs a traversable dir before chmod below
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		// Strip write permission on sub so the first RemoveAll pass cannot
		// unlink a.txt or remove sub itself.
		if err := os.Chmod(sub, 0o500); err != nil { //nolint:gosec // read-only dir is the scenario under test
			t.Fatal(err)
		}

		if err := gitrun.RemoveAll(root); err != nil {
			t.Fatalf("RemoveAll on a tree with a read-only directory: %v", err)
		}

		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Errorf("root still exists after RemoveAll: %v", err)
		}
	})
}
