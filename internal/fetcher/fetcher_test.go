// Copyright 2026 The Graft Authors

package fetcher_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/fetcher"
	"github.com/min0625/graft/internal/gittest"
)

const depName = "scripts"

func fetchDst(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "tree")
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // Test-controlled path.
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func fixtureTime(t *testing.T, r *gittest.Repo, sha string) time.Time {
	t.Helper()

	out := strings.TrimSpace(r.Git("show", "--no-patch", "--format=%cI", sha))

	at, err := time.Parse(time.RFC3339, out)
	if err != nil {
		t.Fatal(err)
	}

	return at.UTC()
}

func TestFetch_tipCommit(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("run.sh", "#!/bin/sh\n")
	r.WriteFile("lib/util.sh", "util\n")
	sha := r.Commit("first")

	dst := fetchDst(t)

	commitTime, err := fetcher.Fetch(t.Context(), depName, r.URL(), sha, "", dst)
	if err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(dst, "run.sh")); got != "#!/bin/sh\n" {
		t.Errorf("run.sh = %q", got)
	}

	if got := readFile(t, filepath.Join(dst, "lib", "util.sh")); got != "util\n" {
		t.Errorf("lib/util.sh = %q", got)
	}

	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Error(".git survived the checkout")
	}

	if want := fixtureTime(t, r, sha); !commitTime.Equal(want) {
		t.Errorf("commit time = %v, want %v", commitTime, want)
	}
}

// TestFetch_nonTipCommit covers the SHA-rejection fallback: a local file://
// remote rejects depth-1 fetches of unadvertised commits, so fetching a
// non-tip commit only succeeds through the full-fetch fallback.
func TestFetch_nonTipCommit(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("a.txt", "old\n")
	old := r.Commit("first")
	r.WriteFile("a.txt", "new\n")
	r.Commit("second")

	dst := fetchDst(t)

	if _, err := fetcher.Fetch(t.Context(), depName, r.URL(), old, "", dst); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(dst, "a.txt")); got != "old\n" {
		t.Errorf("a.txt = %q, want the old content", got)
	}
}

func TestFetch_pathSubtree(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("proto/svc.proto", "service\n")
	r.WriteFile("other/junk.txt", "junk\n")
	sha := r.Commit("first")

	dst := fetchDst(t)

	if _, err := fetcher.Fetch(t.Context(), depName, r.URL(), sha, "proto", dst); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(dst, "svc.proto")); got != "service\n" {
		t.Errorf("svc.proto = %q", got)
	}

	if _, err := os.Stat(filepath.Join(dst, "other")); !os.IsNotExist(err) {
		t.Error("content outside path leaked into dst")
	}
}

func TestFetch_missingPath(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	sha := r.Commit("first")

	_, err := fetcher.Fetch(t.Context(), depName, r.URL(), sha, "nope", fetchDst(t))
	if got := clierr.ExitCode(err); got != int(clierr.CodeConfig) {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeConfig, err)
	}
}

func TestFetch_commitGone(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	r.Commit("first")

	_, err := fetcher.Fetch(t.Context(), depName, r.URL(),
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "", fetchDst(t))
	if got := clierr.ExitCode(err); got != int(clierr.CodeGeneral) {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeGeneral, err)
	}

	if !strings.Contains(clierr.Format(err), "re-pin") {
		t.Errorf("error should hint at re-pinning:\n%s", clierr.Format(err))
	}
}

func TestFetch_unreachableRemote(t *testing.T) {
	t.Parallel()

	gone := gittest.New(t)
	url := gone.URL()

	if err := os.RemoveAll(gone.Dir); err != nil {
		t.Fatal(err)
	}

	_, err := fetcher.Fetch(t.Context(), depName, url,
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "", fetchDst(t))
	if got := clierr.ExitCode(err); got != int(clierr.CodeNetwork) {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeNetwork, err)
	}
}

func TestFetch_rejectsLFS(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile(".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	r.WriteFile("a.txt", "x\n")
	sha := r.Commit("first")

	_, err := fetcher.Fetch(t.Context(), depName, r.URL(), sha, "", fetchDst(t))
	if got := clierr.ExitCode(err); got != int(clierr.CodeConfig) {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeConfig, err)
	}

	if !strings.Contains(err.Error(), depName) {
		t.Errorf("LFS error should name the dep: %v", err)
	}
}

func TestFetch_lfsOutsidePathIsIgnored(t *testing.T) {
	t.Parallel()

	r := gittest.New(t)
	r.WriteFile("media/.gitattributes", "*.bin filter=lfs\n")
	r.WriteFile("proto/svc.proto", "service\n")
	sha := r.Commit("first")

	dst := fetchDst(t)

	if _, err := fetcher.Fetch(t.Context(), depName, r.URL(), sha, "proto", dst); err != nil {
		t.Fatalf("LFS declaration outside the fetched path must not fail the install: %v", err)
	}
}
