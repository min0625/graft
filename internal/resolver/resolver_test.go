// Copyright 2026 The Graft Authors

package resolver_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gittest"
	"github.com/min0625/graft/internal/resolver"
)

func TestPseudoVersion_roundTrip(t *testing.T) {
	t.Parallel()

	commit := "a3f8c21d4e8f1b2c3d4e5f6a7b8c9d0e1f2a3b4c"
	at := time.Date(2026, 4, 18, 9, 13, 27, 0, time.UTC)

	v := resolver.PseudoVersion(at, commit)
	if want := "v0.0.0-20260418091327-a3f8c21d4e8f"; v != want {
		t.Fatalf("PseudoVersion = %q, want %q", v, want)
	}

	gotTime, sha12, ok := resolver.ParsePseudoVersion(v)
	if !ok {
		t.Fatalf("ParsePseudoVersion(%q) not ok", v)
	}

	if sha12 != commit[:12] {
		t.Errorf("sha12 = %q, want %q", sha12, commit[:12])
	}

	if !gotTime.Equal(at) {
		t.Errorf("time = %v, want %v", gotTime, at)
	}
}

func TestParsePseudoVersion_rejectsNonPseudo(t *testing.T) {
	t.Parallel()

	for _, v := range []string{
		"v1.2.0",
		"v0.0.0-2026041809132-a3f8c21d4e8f",  // 13-digit timestamp
		"v0.0.0-20260418091327-a3f8c21d4e8",  // 11-char sha
		"v0.0.0-20260418091327-A3F8C21D4E8F", // uppercase sha
		"v0.0.0-20269918091327-a3f8c21d4e8f", // month 99
		"release-2024",
	} {
		if _, _, ok := resolver.ParsePseudoVersion(v); ok {
			t.Errorf("ParsePseudoVersion(%q) ok, want not ok", v)
		}
	}
}

// fixture creates a remote with a tagged commit, a branch commit, and a tag
// and branch sharing the name "dual" but pointing at different commits.
type fixture struct {
	repo      *gittest.Repo
	tagged    string // commit tagged v1.2.0 (annotated) and dual (lightweight)
	branchTip string // commit at the tip of branch "feature" and "dual"
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	r := gittest.New(t)

	r.WriteFile("a.txt", "one\n")
	tagged := r.Commit("first")
	r.Git("tag", "--annotate", "--message", "release", "v1.2.0")
	r.Git("push", "origin", "v1.2.0")
	r.Tag("dual")

	r.Branch("feature")
	r.WriteFile("a.txt", "two\n")
	branchTip := r.Commit("second")

	// Branch "dual" at branchTip; the tag "dual" stays at tagged. The push
	// needs the full refspec because the short name is ambiguous.
	r.Git("switch", "--create", "dual")
	r.Git("push", "origin", "refs/heads/dual:refs/heads/dual")

	return &fixture{repo: r, tagged: tagged, branchTip: branchTip}
}

func (f *fixture) commitTime(t *testing.T, sha string) time.Time {
	t.Helper()

	out := strings.TrimSpace(f.repo.Git("show", "--no-patch", "--format=%cI", sha))

	at, err := time.Parse(time.RFC3339, out)
	if err != nil {
		t.Fatalf("parse fixture commit time %q: %v", out, err)
	}

	return at.UTC()
}

func checkResolution(t *testing.T, f *fixture, got resolver.Resolution, wantCommit string, wantTag bool) {
	t.Helper()

	if got.Commit != wantCommit {
		t.Errorf("Commit = %q, want %q", got.Commit, wantCommit)
	}

	if got.IsTag != wantTag {
		t.Errorf("IsTag = %v, want %v", got.IsTag, wantTag)
	}

	if want := f.commitTime(t, wantCommit); !got.Time.Equal(want) {
		t.Errorf("Time = %v, want %v", got.Time, want)
	}
}

func TestResolveRef(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	t.Run("annotated tag", func(t *testing.T) {
		t.Parallel()

		got, err := resolver.ResolveRef(t.Context(), f.repo.URL(), "v1.2.0")
		if err != nil {
			t.Fatal(err)
		}

		checkResolution(t, f, got, f.tagged, true)
	})

	t.Run("branch", func(t *testing.T) {
		t.Parallel()

		got, err := resolver.ResolveRef(t.Context(), f.repo.URL(), "feature")
		if err != nil {
			t.Fatal(err)
		}

		checkResolution(t, f, got, f.branchTip, false)
	})

	t.Run("tag wins over branch", func(t *testing.T) {
		t.Parallel()

		got, err := resolver.ResolveRef(t.Context(), f.repo.URL(), "dual")
		if err != nil {
			t.Fatal(err)
		}

		checkResolution(t, f, got, f.tagged, true)
	})

	t.Run("full sha", func(t *testing.T) {
		t.Parallel()

		got, err := resolver.ResolveRef(t.Context(), f.repo.URL(), f.branchTip)
		if err != nil {
			t.Fatal(err)
		}

		checkResolution(t, f, got, f.branchTip, false)
	})

	t.Run("partial sha", func(t *testing.T) {
		t.Parallel()

		got, err := resolver.ResolveRef(t.Context(), f.repo.URL(), f.tagged[:8])
		if err != nil {
			t.Fatal(err)
		}

		checkResolution(t, f, got, f.tagged, false)
	})

	t.Run("nonexistent ref", func(t *testing.T) {
		t.Parallel()

		_, err := resolver.ResolveRef(t.Context(), f.repo.URL(), "no-such-ref")
		if got := clierr.ExitCode(err); got != int(clierr.CodeGeneral) {
			t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeGeneral, err)
		}
	})

	t.Run("nonexistent commit", func(t *testing.T) {
		t.Parallel()

		_, err := resolver.ResolveRef(t.Context(), f.repo.URL(), "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
		if got := clierr.ExitCode(err); got != int(clierr.CodeGeneral) {
			t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeGeneral, err)
		}
	})
}

func TestResolveRef_unreachableRemote(t *testing.T) {
	t.Parallel()

	gone := gittest.New(t)
	url := gone.URL()

	if err := os.RemoveAll(gone.Dir); err != nil {
		t.Fatal(err)
	}

	_, err := resolver.ResolveRef(t.Context(), url, "v1.0.0")
	if got := clierr.ExitCode(err); got != int(clierr.CodeNetwork) {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeNetwork, err)
	}
}

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	t.Run("tag", func(t *testing.T) {
		t.Parallel()

		got, err := resolver.ResolveVersion(t.Context(), f.repo.URL(), "v1.2.0")
		if err != nil {
			t.Fatal(err)
		}

		checkResolution(t, f, got, f.tagged, true)
	})

	t.Run("pseudo-version", func(t *testing.T) {
		t.Parallel()

		v := resolver.PseudoVersion(f.commitTime(t, f.branchTip), f.branchTip)

		got, err := resolver.ResolveVersion(t.Context(), f.repo.URL(), v)
		if err != nil {
			t.Fatal(err)
		}

		checkResolution(t, f, got, f.branchTip, false)
	})

	t.Run("branch name is not a version", func(t *testing.T) {
		t.Parallel()

		_, err := resolver.ResolveVersion(t.Context(), f.repo.URL(), "feature")
		if got := clierr.ExitCode(err); got != int(clierr.CodeGeneral) {
			t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeGeneral, err)
		}
	})
}

func TestResolveLatest(t *testing.T) {
	t.Parallel()

	t.Run("picks highest semver tag", func(t *testing.T) {
		t.Parallel()

		r := gittest.New(t)
		r.WriteFile("f.txt", "a\n")
		r.Commit("v1")
		r.Tag("v1.0.0")
		r.WriteFile("f.txt", "b\n")
		hi := r.Commit("v2")
		r.Tag("v2.0.0")

		res, tag, err := resolver.ResolveLatest(t.Context(), r.URL())
		if err != nil {
			t.Fatal(err)
		}

		if tag != "v2.0.0" {
			t.Errorf("tag = %q, want v2.0.0", tag)
		}

		if res.Commit != hi {
			t.Errorf("commit = %q, want %q", res.Commit, hi)
		}

		if !res.IsTag {
			t.Error("IsTag = false, want true")
		}
	})

	t.Run("skips pre-release", func(t *testing.T) {
		t.Parallel()

		r := gittest.New(t)
		r.WriteFile("f.txt", "a\n")
		stable := r.Commit("v1")
		r.Tag("v1.0.0")
		r.WriteFile("f.txt", "b\n")
		r.Commit("rc")
		r.Tag("v2.0.0-rc.1")

		res, tag, err := resolver.ResolveLatest(t.Context(), r.URL())
		if err != nil {
			t.Fatal(err)
		}

		if tag != "v1.0.0" {
			t.Errorf("tag = %q, want v1.0.0 (pre-release should be skipped)", tag)
		}

		if res.Commit != stable {
			t.Errorf("commit = %q, want %q", res.Commit, stable)
		}
	})

	t.Run("accepts tag without v prefix", func(t *testing.T) {
		t.Parallel()

		r := gittest.New(t)
		r.WriteFile("f.txt", "a\n")
		c := r.Commit("v1")
		r.Tag("1.5.0")

		_, tag, err := resolver.ResolveLatest(t.Context(), r.URL())
		if err != nil {
			t.Fatal(err)
		}

		if tag != "1.5.0" {
			t.Errorf("tag = %q, want 1.5.0", tag)
		}

		_ = c
	})

	t.Run("fallback to HEAD when no semver tags", func(t *testing.T) {
		t.Parallel()

		r := gittest.New(t)
		r.WriteFile("f.txt", "a\n")
		head := r.Commit("init")
		r.Tag("release-2024") // non-semver tag

		res, tag, err := resolver.ResolveLatest(t.Context(), r.URL())
		if err != nil {
			t.Fatal(err)
		}

		if tag != "" {
			t.Errorf("tag = %q, want empty (should fall back to HEAD)", tag)
		}

		if res.Commit != head {
			t.Errorf("commit = %q, want %q", res.Commit, head)
		}
	})
}
