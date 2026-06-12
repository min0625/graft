// Copyright 2026 The Graft Authors

package gittest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/gittest"
)

func TestRepo_commitTagBranch(t *testing.T) {
	t.Parallel()

	repo := gittest.New(t)

	repo.WriteFile("README.md", "hello\n")
	repo.WriteFile("scripts/build.sh", "#!/bin/sh\n")
	first := repo.Commit("initial commit")

	if len(first) != 40 {
		t.Fatalf("Commit returned %q, want a full 40-char SHA", first)
	}

	repo.Tag("v1.0.0")

	repo.Branch("feature")
	repo.WriteFile("README.md", "hello from feature\n")
	second := repo.Commit("feature commit")

	if second == first {
		t.Fatalf("second commit SHA equals first (%s)", first)
	}

	refs := repo.Git("ls-remote", repo.URL())

	wantRefs := map[string]string{
		"refs/heads/main":    first,
		"refs/tags/v1.0.0":   first,
		"refs/heads/feature": second,
	}
	for ref, sha := range wantRefs {
		if !strings.Contains(refs, sha+"\t"+ref) {
			t.Errorf("ls-remote missing %s -> %s:\n%s", ref, sha, refs)
		}
	}
}

func TestRepo_checkoutSwitchesBranch(t *testing.T) {
	t.Parallel()

	repo := gittest.New(t)

	repo.WriteFile("file.txt", "main\n")
	mainSHA := repo.Commit("on main")

	repo.Branch("dev")
	repo.WriteFile("file.txt", "dev\n")
	repo.Commit("on dev")

	repo.Checkout("main")

	head := strings.TrimSpace(repo.Git("rev-parse", "HEAD"))
	if head != mainSHA {
		t.Errorf("after Checkout(main) HEAD = %s, want %s", head, mainSHA)
	}
}

func TestRepo_urlIsCloneable(t *testing.T) {
	t.Parallel()

	repo := gittest.New(t)

	repo.WriteFile("dir/data.txt", "fixture content\n")
	repo.Commit("add data")

	dest := filepath.Join(t.TempDir(), "clone")
	repo.Git("clone", repo.URL(), dest)

	got, err := os.ReadFile(filepath.Join(dest, "dir", "data.txt")) //nolint:gosec // Path is inside t.TempDir().
	if err != nil {
		t.Fatalf("read cloned file: %v", err)
	}

	if string(got) != "fixture content\n" {
		t.Errorf("cloned content = %q, want %q", got, "fixture content\n")
	}
}

func TestRepo_urlEncodesSpecialCharacters(t *testing.T) {
	t.Parallel()

	// testing.TempDir keeps "%" from the subtest name, so the repo path
	// contains "%6f". Git percent-decodes file:// URLs, so an unencoded URL
	// would point at the wrong (decoded) path.
	t.Run("pct%6fdir", func(t *testing.T) {
		t.Parallel()

		repo := gittest.New(t)

		repo.WriteFile("file.txt", "data\n")
		sha := repo.Commit("add file")

		refs := repo.Git("ls-remote", repo.URL())
		if !strings.Contains(refs, sha) {
			t.Errorf("ls-remote via URL() missing %s:\n%s", sha, refs)
		}
	})
}
