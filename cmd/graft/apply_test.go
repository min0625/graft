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
	"github.com/min0625/graft/internal/gittest"
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
// tree is drift and is reinstalled from the locked commit (spec §4.4, §10.5).
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

// TestApply_cwdInsideDepGivesClearError covers the low-severity Windows bug:
// reinstalling a dep whose vendor tree contains the process's current
// working directory used to surface as a raw OS-level "used by another
// process"/"not a directory" error with no indication of the cause. It must
// now fail with a clear message telling the user to cd out first.
//
// spec: REQ-APPLY-CWD-GUARD
func TestApply_cwdInsideDepGivesClearError(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")
	mustRunGraft(t, "apply")

	// Tamper so the next apply must reinstall the dep.
	writeProjectFile(t, dir, runShPath, "tampered\n")

	t.Chdir(filepath.Join(dir, "deps", depScripts))

	_, stderr, exitCode := runGraftStreams(t, "apply")
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}

	if !strings.Contains(stderr, "current directory is inside it") {
		t.Errorf("stderr = %q, want a clear cwd-inside-target error", stderr)
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

	b.WriteString("dir = \"deps\"\n")

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
// spec §5.2 worker pool; the suite runs under -race, so any data race in the
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
// and reported together (spec §5.2 — no fail-fast), so doctoring the hash of
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

	writeProjectFile(t, dir, "graft.toml", `dir = "deps"`)
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
// removing the remote before applying (spec §5.4).
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

// TestLock_rejectsSymlinkWithPath verifies the exit-2 error message names the
// symlink. The fixture stages the symlink as a raw 120000 index entry, so the
// test runs on every platform — the rejection is decided from the git tree
// mode before checkout, never from the checked-out filesystem.
// spec: REQ-HASH-SYMLINK-PATH
// spec: REQ-FETCH-SYMLINK-MODE
func TestLock_rejectsSymlinkWithPath(t *testing.T) {
	r := gittest.New(t)
	r.WriteFile("run.sh", "v1\n")
	r.Git("add", "--all")
	r.IndexEntry("120000", "link.sh", "run.sh")
	r.CommitIndex("add symlink")
	r.Tag("v1.0.0")

	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", `dir = "deps"

[[deps]]
name    = "scripts"
repo    = "`+r.URL()+`"
version = "v1.0.0"
`)

	_, stderr, exitCode := runGraftStreams(t, "lock")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}

	if !strings.Contains(stderr, "link.sh") {
		t.Errorf("error output %q does not mention symlink path %q", stderr, "link.sh")
	}
}

// TestAdd_symlinksSkipFlag verifies `graft add --symlinks=skip` adds a repo
// that contains a symlink in one shot, writing symlinks="skip" to graft.toml.
// spec: REQ-ADD-SYMLINKS
func TestAdd_symlinksSkipFlag(t *testing.T) {
	r := gittest.New(t)
	r.WriteFile("run.sh", "v1\n")
	r.Git("add", "--all")
	r.IndexEntry("120000", "link.sh", "run.sh")
	r.CommitIndex("add symlink")
	r.Tag("v1.0.0")

	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	mustRunGraft(t, "add", "--name", "scripts", "--symlinks=skip", r.URL()+"@v1.0.0")

	m := loadManifestFor(t, dir)
	if len(m.Deps) != 1 || !m.Deps[0].SkipSymlinks() {
		t.Fatalf(`manifest deps = %+v, want one dep with symlinks="skip"`, m.Deps)
	}

	if got := readProjectFile(t, dir, "deps/scripts/run.sh"); got != "v1\n" {
		t.Errorf("run.sh = %q, want %q", got, "v1\n")
	}

	if _, err := os.Lstat(filepath.Join(dir, "deps", "scripts", "link.sh")); err == nil {
		t.Errorf("symlink link.sh should not exist in vendor, but it does")
	}
}

// TestApply_symlinksSkip verifies a dep with symlinks="skip" installs without symlinks.
// The symlink is staged as a raw 120000 index entry, so the test runs on
// every platform: with graft's forced core.symlinks=false the entry checks
// out as a placeholder file, which must be deleted by tree path and never
// reach the hash, the store, or the vendor directory.
//
// spec: REQ-DEP-SYMLINKS
// spec: REQ-FETCH-SYMLINK-MODE
func TestApply_symlinksSkip(t *testing.T) {
	r := gittest.New(t)
	r.WriteFile("run.sh", "v1\n")
	r.Git("add", "--all")
	r.IndexEntry("120000", "link.sh", "run.sh")
	r.CommitIndex("add symlink")
	r.Tag("v1.0.0")

	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", `dir = "deps"

[[deps]]
name     = "scripts"
repo     = "`+r.URL()+`"
version  = "v1.0.0"
symlinks = "skip"
`)

	mustRunGraft(t, "lock")
	mustRunGraft(t, "apply")

	// The real file should be installed.
	if got := readProjectFile(t, dir, "deps/scripts/run.sh"); got != "v1\n" {
		t.Errorf("run.sh = %q, want %q", got, "v1\n")
	}

	// The symlink should NOT be in vendor.
	symlinkPath := filepath.Join(dir, "deps", "scripts", "link.sh")
	if _, err := os.Lstat(symlinkPath); err == nil {
		t.Errorf("symlink link.sh should not exist in vendor, but it does")
	}
}

// TestApply_symlinksPolicyOutOfSync: dropping `symlinks = "skip"` from the
// manifest without re-locking is drift. apply must fail "out of sync"
// regardless of store state, so a warm-store machine and a cold CI box agree
// (spec §4.4).
//
// spec: REQ-APPLY-SYMLINKS-SYNC
func TestApply_symlinksPolicyOutOfSync(t *testing.T) {
	r := gittest.New(t)
	r.WriteFile("run.sh", "v1\n")
	r.Git("add", "--all")
	r.IndexEntry("120000", "link.sh", "run.sh")
	r.CommitIndex("add symlink")
	r.Tag("v1.0.0")

	withSymlinks := `dir = "deps"

[[deps]]
name     = "scripts"
repo     = "` + r.URL() + `"
version  = "v1.0.0"
symlinks = "skip"
`

	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", withSymlinks)
	mustRunGraft(t, "lock") // warms the store with the symlink-stripped tree

	// Remove `symlinks = "skip"` (back to the default reject) without re-locking.
	writeProjectFile(t, dir, "graft.toml", `dir = "deps"

[[deps]]
name    = "scripts"
repo    = "`+r.URL()+`"
version = "v1.0.0"
`)

	_, err := runGraft(t, "apply")
	wantExit(t, err, clierr.CodeConfig)

	if msg := clierr.Format(err); !strings.Contains(msg, "out of sync") {
		t.Errorf("error = %s", msg)
	}
}

// TestAdd_rejectsGitSubmodule: a dependency tree containing a git-submodule
// entry (mode 160000) is rejected with exit 2 naming the entry's path — a
// plain checkout would silently omit the submodule's content.
// spec: REQ-FETCH-GITLINK
func TestAdd_rejectsGitSubmodule(t *testing.T) {
	r := gittest.New(t)
	r.WriteFile("a.txt", "x\n")
	r.Git("add", "--all")
	r.GitlinkEntry("sub/module")
	r.CommitIndex("add submodule")
	r.Tag("v1.0.0")

	newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	_, stderr, exitCode := runGraftStreams(t, "add", r.URL()+"@v1.0.0")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitCode, stderr)
	}

	if !strings.Contains(stderr, "submodule") || !strings.Contains(stderr, "sub/module") {
		t.Errorf("error output %q should name the submodule entry %q", stderr, "sub/module")
	}
}

// TestLock_rejectsTreePathCollision: case-colliding paths are rejected at
// lock from the git tree paths, before checkout. The fixture stages both
// spellings as raw index entries — on a case-insensitive filesystem they
// cannot even be authored side by side, and a checkout would silently merge
// them, so only a tree-path check can catch this on every platform.
// spec: REQ-FETCH-PATHCHECK
func TestLock_rejectsTreePathCollision(t *testing.T) {
	r := gittest.New(t)
	r.IndexEntry("100644", "README.md", "upper\n")
	r.IndexEntry("100644", "readme.md", "lower\n")
	r.CommitIndex("add colliding files")
	r.Tag("v1.0.0")

	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", `dir = "deps"

[[deps]]
name    = "colliding"
repo    = "`+r.URL()+`"
version = "v1.0.0"
`)

	_, stderr, exitCode := runGraftStreams(t, "lock")
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", exitCode, stderr)
	}

	if !strings.Contains(stderr, "path collision") {
		t.Errorf("error output %q should report a path collision", stderr)
	}
}

// TestApply_deepPathsExceedWindowsMaxPath: a dependency whose paths exceed
// Windows' traditional 260-char MAX_PATH still locks and installs — graft's
// checkouts force core.longpaths=true. On POSIX this passes trivially; on
// Windows CI it exercises the real limit.
// spec: REQ-FETCH-LONGPATHS
func TestApply_deepPathsExceedWindowsMaxPath(t *testing.T) {
	seg := strings.Repeat("d", 40)
	deep := strings.TrimSuffix(strings.Repeat(seg+"/", 7), "/") // 286 chars

	r := gittest.New(t)
	r.WriteFile(deep+"/leaf.txt", "deep\n")
	r.Commit("deep tree")
	r.Tag("v1.0.0")

	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", r.URL()+"@v1.0.0")

	if got := readProjectFile(t, dir, "deps/remote/"+deep+"/leaf.txt"); got != "deep\n" {
		t.Errorf("leaf.txt = %q, want %q", got, "deep\n")
	}
}
