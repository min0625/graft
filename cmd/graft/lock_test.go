// Copyright 2026 The Graft Authors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/lockfile"
)

func loadLockFor(t *testing.T, dir string) *lockfile.Lockfile {
	t.Helper()

	lf, err := lockfile.Load(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatal(err)
	}

	return lf
}

func TestLock_newDep(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	out := mustRunGraft(t, "lock")

	if !strings.Contains(out, "✓ updated graft.lock") {
		t.Errorf("output = %q", out)
	}

	lf := loadLockFor(t, dir)
	if len(lf.Deps) != 1 {
		t.Fatalf("Deps = %+v", lf.Deps)
	}

	dep := lf.Deps[0]
	if dep.Commit != f.v1 {
		t.Errorf("Commit = %q, want %q", dep.Commit, f.v1)
	}

	if got := lf.Dest(dep); got != "deps/scripts" {
		t.Errorf("Dest = %q", got)
	}

	if !strings.HasPrefix(dep.Hash, "sha256:") {
		t.Errorf("Hash = %q", dep.Hash)
	}

	if dep.Time.IsZero() {
		t.Error("Time is zero")
	}

	// `graft lock` must not install anything.
	if _, err := os.Stat(filepath.Join(dir, "deps")); !os.IsNotExist(err) {
		t.Error("lock created the vendor directory")
	}
}

func TestLock_changedVersionReResolves(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV2))
	mustRunGraft(t, "lock")

	if got := loadLockFor(t, dir).Deps[0].Commit; got != f.v2 {
		t.Errorf("Commit = %q, want %q", got, f.v2)
	}
}

func TestLock_changedRepoReResolves(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	// A second remote with the same tag pointing at a different commit.
	other := newFixtureRemote(t)

	writeProjectFile(t, dir, "graft.toml", strings.Replace(
		manifestFor(f, tagV1), f.repo.URL(), other.repo.URL(), 1))
	mustRunGraft(t, "lock")

	got := loadLockFor(t, dir).Deps[0]
	if got.Commit != other.v1 {
		t.Errorf("Commit = %q, want %q from the new repo", got.Commit, other.v1)
	}
}

func TestLock_changedPathOnlyKeepsCommit(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	before := loadLockFor(t, dir).Deps[0]

	// Deleting the tag proves the re-lock does no ref lookup: the commit
	// stays reachable through the branch, but the version no longer
	// resolves.
	f.repo.Git("tag", "--delete", tagV1)
	f.repo.Git("push", "origin", ":refs/tags/v1.0.0")

	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1)+`path = "sub"`+"\n")
	mustRunGraft(t, "lock")

	after := loadLockFor(t, dir).Deps[0]

	if after.Commit != before.Commit {
		t.Errorf("Commit changed: %q → %q", before.Commit, after.Commit)
	}

	if after.Hash == before.Hash {
		t.Error("Hash unchanged although path changed")
	}

	if after.Path != "sub" {
		t.Errorf("Path = %q", after.Path)
	}
}

func TestLock_unchangedDepNeedsNoNetwork(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	before := readProjectFile(t, dir, "graft.lock")

	// An unreachable remote proves the unchanged entry is kept offline.
	if err := os.RemoveAll(f.repo.Dir); err != nil {
		t.Fatal(err)
	}

	out := mustRunGraft(t, "lock")

	if !strings.Contains(out, "✓ graft.lock is up to date") {
		t.Errorf("output = %q", out)
	}

	if after := readProjectFile(t, dir, "graft.lock"); after != before {
		t.Errorf("lockfile changed:\n%s\n---\n%s", before, after)
	}
}

func TestLock_removedDep(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	writeProjectFile(t, dir, "graft.toml", `vendor = "deps"`)
	mustRunGraft(t, "lock")

	if deps := loadLockFor(t, dir).Deps; len(deps) != 0 {
		t.Errorf("Deps = %+v, want none", deps)
	}
}

func TestLock_missingManifest(t *testing.T) {
	newProjectDir(t)

	_, err := runGraft(t, "lock")
	wantExit(t, err, clierr.CodeConfig)

	if !strings.Contains(clierr.Format(err), "graft init") {
		t.Errorf("error should hint at graft init:\n%s", clierr.Format(err))
	}
}

func TestLockCheck_inSync(t *testing.T) {
	// spec: REQ-LOCKCHECK-INSYNC
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	mustRunGraft(t, "lock")

	out := mustRunGraft(t, "lock", "--check")

	if !strings.Contains(out, "✓") || !strings.Contains(out, "up to date") {
		t.Errorf("output = %q, want success message", out)
	}

	if fi, err := os.Stat(filepath.Join(dir, lockfile.Filename)); err != nil {
		t.Fatalf("lock file disappeared: %v", err)
	} else {
		_ = fi // just checking it exists
	}
}

func TestLockCheck_missingLock(t *testing.T) {
	// spec: REQ-LOCKCHECK-MISSING
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	_, err := runGraft(t, "lock", "--check")
	wantExit(t, err, clierr.CodeConfig)

	if !strings.Contains(clierr.Format(err), "graft lock") {
		t.Errorf("error should hint at graft lock:\n%s", clierr.Format(err))
	}

	_ = dir
}

func TestLockCheck_newDepNotLocked(t *testing.T) {
	// spec: REQ-LOCKCHECK-OUTOFDATE
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	mustRunGraft(t, "lock")

	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1)+`
[[deps]]
name    = "extra"
repo    = "`+f.repo.URL()+`"
version = "`+tagV2+`"
`)

	_, err := runGraft(t, "lock", "--check")
	wantExit(t, err, clierr.CodeConfig)

	msg := clierr.Format(err)
	if !strings.Contains(msg, "extra") {
		t.Errorf("error should name the out-of-date dep:\n%s", msg)
	}
}

func TestLockCheck_changedVersion(t *testing.T) {
	// spec: REQ-LOCKCHECK-OUTOFDATE
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	mustRunGraft(t, "lock")

	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV2))

	_, err := runGraft(t, "lock", "--check")
	wantExit(t, err, clierr.CodeConfig)

	msg := clierr.Format(err)
	if !strings.Contains(msg, "scripts") {
		t.Errorf("error should name the out-of-date dep:\n%s", msg)
	}

	_ = dir
}

func TestLockCheck_doesNotModifyLock(t *testing.T) {
	// spec: REQ-LOCKCHECK-NOWRITE
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	mustRunGraft(t, "lock")

	beforeStat, err := os.Stat(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatal(err)
	}

	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV2))

	runGraft(t, "lock", "--check") //nolint:errcheck,gosec // exit 2 expected, not a test failure

	afterStat, err := os.Stat(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatal(err)
	}

	if beforeStat.ModTime() != afterStat.ModTime() {
		t.Error("lock --check must not modify graft.lock")
	}
}
