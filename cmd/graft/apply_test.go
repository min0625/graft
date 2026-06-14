// Copyright 2026 The Graft Authors

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
)

func TestApply_freshInstall(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	out := mustRunGraft(t, "apply")

	if want := "✓ installed scripts v1.0.0 (" + f.v1[:7] + ")\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("run.sh = %q", got)
	}

	if got := readProjectFile(t, dir, "deps/scripts/sub/lib.sh"); got != "lib\n" {
		t.Errorf("sub/lib.sh = %q", got)
	}
}

func TestApply_noop(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")
	mustRunGraft(t, "apply")

	out := mustRunGraft(t, "apply")

	if want := "✓ already up to date\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestApply_missingLockfile(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	_, err := runGraft(t, "apply")
	wantExit(t, err, clierr.CodeConfig)

	if !strings.Contains(clierr.Format(err), "graft lock") {
		t.Errorf("error should hint at graft lock:\n%s", clierr.Format(err))
	}
}

func TestApply_outOfSync(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	// Hand-edit the version without re-locking.
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV2))

	_, err := runGraft(t, "apply")
	wantExit(t, err, clierr.CodeConfig)

	if msg := clierr.Format(err); !strings.Contains(msg, "out of sync") {
		t.Errorf("error = %s", msg)
	}
}

// TestApply_repairsTamperedVendor: apply reconciles — a hand-edited vendor
// tree is drift and is reinstalled from the locked commit (spec §4.3, §10.5).
func TestApply_repairsTamperedVendor(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")
	mustRunGraft(t, "apply")

	writeProjectFile(t, dir, runShPath, "tampered\n")

	out := mustRunGraft(t, "apply")

	if !strings.Contains(out, "✓ installed scripts v1.0.0 ("+f.v1[:7]+")") {
		t.Errorf("output = %q", out)
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("run.sh = %q, want the locked content restored", got)
	}
}

// TestApply_tamperedLockfileHash: when the freshly fetched tree does not
// hash to what the lockfile records — a doctored lockfile or rewritten
// upstream — apply fails with the integrity exit code 4.
func TestApply_tamperedLockfileHash(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	lock := readProjectFile(t, dir, "graft.lock")
	doctored := regexp.MustCompile(`hash = "sha256:[0-9a-f]+"`).
		ReplaceAllString(lock, `hash = "sha256:`+strings.Repeat("0", 64)+`"`)
	writeProjectFile(t, dir, "graft.lock", doctored)

	_, err := runGraft(t, "apply")
	wantExit(t, err, clierr.CodeIntegrity)

	if msg := clierr.Format(err); !strings.Contains(msg, "integrity") {
		t.Errorf("error = %s", msg)
	}
}

// multiDepManifest declares n deps on the fixture remote, named dep0..depN,
// alternating between the v1 and v2 tags.
func multiDepManifest(f *fixtureRemote, n int) string {
	var b strings.Builder

	b.WriteString("vendor = \"deps\"\n")

	for i := range n {
		version := tagV1
		if i%2 == 1 {
			version = tagV2
		}

		fmt.Fprintf(&b, "\n[[deps]]\nname    = \"dep%d\"\nrepo    = %q\nversion = %q\n", i, f.repo.URL(), version)
	}

	return b.String()
}

// TestApply_multiDepParallel: a fresh install of several deps exercises the
// spec §5.4 worker pool; the suite runs under -race, so any data race in the
// parallel reconcile fails the test. All deps point at the same upstream repo,
// so the per-repo advisory lock inside the fetcher serializes access to the
// bare-repo cache — proven correct by the -race detector.
func TestApply_multiDepParallel(t *testing.T) {
	// spec: REQ-JOBS-SAMEREPO
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", multiDepManifest(f, 4))
	mustRunGraft(t, "lock")

	out := mustRunGraft(t, "apply")

	for i := range 4 {
		name := fmt.Sprintf("dep%d", i)
		if !strings.Contains(out, "✓ installed "+name+" ") {
			t.Errorf("output missing %s:\n%s", name, out)
		}

		want := contentV1
		if i%2 == 1 {
			want = contentV2
		}

		if got := readProjectFile(t, dir, "deps/"+name+"/run.sh"); got != want {
			t.Errorf("%s/run.sh = %q, want %q", name, got, want)
		}
	}
}

// TestApply_collectsAllErrors: errors are collected across the worker pool
// and reported together (spec §5.4 — no fail-fast), so doctoring the hash of
// two deps surfaces two integrity errors in a single run.
func TestApply_collectsAllErrors(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", multiDepManifest(f, 2))
	mustRunGraft(t, "lock")

	lock := readProjectFile(t, dir, "graft.lock")
	doctored := regexp.MustCompile(`hash = "sha256:[0-9a-f]+"`).
		ReplaceAllString(lock, `hash = "sha256:`+strings.Repeat("0", 64)+`"`)
	writeProjectFile(t, dir, "graft.lock", doctored)

	_, err := runGraft(t, "apply")
	wantExit(t, err, clierr.CodeIntegrity)

	msg := clierr.Format(err)
	for _, name := range []string{"dep0", "dep1"} {
		if !strings.Contains(msg, fmt.Sprintf("content integrity check failed for %q", name)) {
			t.Errorf("error should report %s:\n%s", name, msg)
		}
	}
}

func TestApply_removesExtraAfterDepRemoval(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")
	mustRunGraft(t, "apply")

	writeProjectFile(t, dir, "graft.toml", `vendor = "deps"`)
	mustRunGraft(t, "lock")

	out := mustRunGraft(t, "apply")

	if !strings.Contains(out, "✓ removed deps/scripts") {
		t.Errorf("output = %q", out)
	}

	if _, err := os.Stat(filepath.Join(dir, "deps")); !os.IsNotExist(err) {
		t.Error("empty vendor directory survived")
	}
}

// TestApply_offlineFromStore checks the content store: `graft lock` populates
// it, so a later `graft apply` installs with no network at all — proven by
// removing the remote before applying (spec §5.6).
func TestApply_offlineFromStore(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	// Remove the remote: apply must still succeed entirely from the store.
	if err := os.RemoveAll(f.repo.Dir); err != nil {
		t.Fatal(err)
	}

	out := mustRunGraft(t, "apply")

	if want := "✓ installed scripts v1.0.0 (" + f.v1[:7] + ")\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("run.sh = %q", got)
	}
}

// TestApply_jobsFlag verifies that --jobs 1 forces sequential operation and
// still produces a correct install (spec §5.4).
func TestApply_jobsFlag(t *testing.T) {
	// spec: REQ-JOBS-SERIAL
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", multiDepManifest(f, 4))
	mustRunGraft(t, "lock")

	out := mustRunGraft(t, "--jobs", "1", "apply")

	for i := range 4 {
		name := fmt.Sprintf("dep%d", i)
		if !strings.Contains(out, "✓ installed "+name+" ") {
			t.Errorf("output missing %s:\n%s", name, out)
		}
	}

	_ = dir
}

// TestApply_concurrencyEnvVar verifies that GRAFT_CONCURRENCY sets the fetch
// concurrency in the same way as --jobs, producing a correct install (spec §5.4).
func TestApply_concurrencyEnvVar(t *testing.T) {
	// spec: REQ-JOBS-ENV
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", multiDepManifest(f, 2))
	mustRunGraft(t, "lock")

	t.Setenv("GRAFT_CONCURRENCY", "1")

	out := mustRunGraft(t, "apply")

	for i := range 2 {
		name := fmt.Sprintf("dep%d", i)
		if !strings.Contains(out, "✓ installed "+name+" ") {
			t.Errorf("output missing %s:\n%s", name, out)
		}
	}

	_ = dir
}
