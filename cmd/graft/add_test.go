// Copyright 2026 The Graft Authors

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
)

const (
	ghRepo  = "github.com/org/repo"
	sshRepo = "git@github.com:org/repo.git"

	// depRemote is the dep name auto-derived from the fixture remote URL
	// (file:///.../remote.git → "remote").
	depRemote = "remote"

	// nameTools is a custom dep name used by the --name tests.
	nameTools = "tools"
)

var pseudoVersionRe = regexp.MustCompile(`^v0\.0\.0-\d{14}-[0-9a-f]{12}$`)

func loadManifestFor(t *testing.T, dir string) *config.Manifest {
	t.Helper()

	m, err := config.Load(dir + "/" + config.Filename)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func TestAdd_newDepByTag(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	out := mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	if !strings.Contains(out, "✓ added remote v1.0.0 ("+f.v1[:7]+")") {
		t.Errorf("output = %q", out)
	}

	m := loadManifestFor(t, dir)
	if len(m.Deps) != 1 || m.Deps[0].Version != tagV1 || m.Deps[0].Name != depRemote {
		t.Errorf("manifest deps = %+v", m.Deps)
	}

	lf := loadLockFor(t, dir)
	if lf.Deps[0].Commit != f.v1 {
		t.Errorf("locked commit = %q, want %q", lf.Deps[0].Commit, f.v1)
	}

	if got := readProjectFile(t, dir, "deps/remote/run.sh"); got != contentV1 {
		t.Errorf("installed run.sh = %q", got)
	}
}

func TestAdd_newDepByBranch(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	mustRunGraft(t, "add", f.repo.URL()+"@dev")

	m := loadManifestFor(t, dir)
	if v := m.Deps[0].Version; !pseudoVersionRe.MatchString(v) {
		t.Errorf("version = %q, want a pseudo-version", v)
	}

	if got := loadLockFor(t, dir).Deps[0].Commit; got != f.dev {
		t.Errorf("locked commit = %q, want branch tip %q", got, f.dev)
	}

	if got := readProjectFile(t, dir, "deps/remote/dev.txt"); got != "dev\n" {
		t.Errorf("dev.txt = %q", got)
	}
}

func TestAdd_newDepByPartialSHA(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	mustRunGraft(t, "add", f.repo.URL()+"@"+f.v1[:10])

	m := loadManifestFor(t, dir)
	if v := m.Deps[0].Version; !pseudoVersionRe.MatchString(v) {
		t.Errorf("version = %q, want a pseudo-version", v)
	}

	if got := loadLockFor(t, dir).Deps[0].Commit; got != f.v1 {
		t.Errorf("locked commit = %q, want %q", got, f.v1)
	}
}

func TestAdd_updateByRepo(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	out := mustRunGraft(t, "add", f.repo.URL()+"@v2.0.0")

	if !strings.Contains(out, "✓ updated remote to v2.0.0 ("+f.v2[:7]+")") {
		t.Errorf("output = %q", out)
	}

	if got := readProjectFile(t, dir, "deps/remote/run.sh"); got != contentV2 {
		t.Errorf("run.sh = %q, want the v2 content", got)
	}
}

func TestAdd_sameCommitIsNoop(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	before := readProjectFile(t, dir, "graft.lock")

	// Re-adding the same commit by SHA keeps the stored tag version.
	out := mustRunGraft(t, "add", f.repo.URL()+"@"+f.v1)

	if !strings.Contains(out, "✓ remote already at "+f.v1[:7]+" (v1.0.0)") {
		t.Errorf("output = %q", out)
	}

	if loadManifestFor(t, dir).Deps[0].Version != tagV1 {
		t.Error("no-op add rewrote the stored version")
	}

	if after := readProjectFile(t, dir, "graft.lock"); after != before {
		t.Errorf("no-op add changed the lockfile:\n%s\n---\n%s", before, after)
	}
}

func TestAdd_missingRef(t *testing.T) {
	// With an unreachable remote, omitting the ref should fail with a network
	// error (ResolveLatest needs to contact the remote to find tags).
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	_, err := runGraft(t, "add", ghRepo)
	wantExit(t, err, clierr.CodeNetwork)
}

func TestAdd_latestParsesHighestTag(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	// Add without a ref: should pick the highest semver tag (v2.0.0).
	out := mustRunGraft(t, "add", f.repo.URL())

	if !strings.Contains(out, "added") {
		t.Errorf("output = %q", out)
	}

	m := loadManifestFor(t, dir)
	if len(m.Deps) == 0 || m.Deps[0].Version != tagV2 {
		t.Errorf("manifest version = %v, want %q", m.Deps, tagV2)
	}
}

func TestAdd_latestExplicit(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	out := mustRunGraft(t, "add", f.repo.URL()+"@latest")

	if !strings.Contains(out, "added remote "+tagV2) {
		t.Errorf("output = %q", out)
	}

	m := loadManifestFor(t, dir)
	if len(m.Deps) == 0 || m.Deps[0].Version != tagV2 {
		t.Errorf("manifest version = %q, want %q", func() string {
			if len(m.Deps) == 0 {
				return "<empty>"
			}

			return m.Deps[0].Version
		}(), tagV2)
	}
}

func TestAdd_resolvesPreexistingDrift(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	// No lockfile yet — add must not fail on the toml ↔ lock mismatch.
	other := newFixtureRemote(t)
	mustRunGraft(t, "add", other.repo.URL()+"@"+tagV1)

	lf := loadLockFor(t, dir)
	if len(lf.Deps) != 2 {
		t.Fatalf("lock deps = %+v, want both deps locked", lf.Deps)
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("pre-existing dep not installed: %q", got)
	}

	if got := readProjectFile(t, dir, "deps/remote/run.sh"); got != contentV1 {
		t.Errorf("added dep not installed: %q", got)
	}
}

func TestSplitSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		spec, base, ref string
	}{
		{"github.com/org/repo@v1.2.0", ghRepo, "v1.2.0"},
		{"github.com/org/repo@feature/x", ghRepo, "feature/x"},
		{ghRepo, ghRepo, ""},
		{sshRepo, sshRepo, ""},
		{"git@github.com:org/repo.git@v1.0.0", sshRepo, tagV1},
		{"scripts@a3f8c21d", "scripts", "a3f8c21d"},
	}

	for _, tt := range tests {
		base, ref := splitSpec(tt.spec)
		if base != tt.base || ref != tt.ref {
			t.Errorf("splitSpec(%q) = (%q, %q), want (%q, %q)", tt.spec, base, ref, tt.base, tt.ref)
		}
	}
}

func TestNormalizeRepo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, want string
	}{
		{"https://github.com/org/repo", ghRepo},
		{"https://github.com/org/repo/", ghRepo},
		{ghRepo, ghRepo},
		{sshRepo, sshRepo},
		{"file:///tmp/fixture/remote.git", "file:///tmp/fixture/remote.git"},
	}

	for _, tt := range tests {
		if got := normalizeRepo(tt.in); got != tt.want {
			t.Errorf("normalizeRepo(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAdd_pathSubtree(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--path", subPath)

	if got := loadManifestFor(t, dir).Deps[0].Path; got != subPath {
		t.Errorf("manifest path = %q, want %q", got, subPath)
	}

	if got := loadLockFor(t, dir).Deps[0].Path; got != subPath {
		t.Errorf("locked path = %q, want %q", got, subPath)
	}

	if got := readProjectFile(t, dir, "deps/remote/lib.sh"); got != contentLib {
		t.Errorf("lib.sh = %q", got)
	}

	if _, err := os.Stat(filepath.Join(dir, "deps", "remote", "run.sh")); !os.IsNotExist(err) {
		t.Errorf("run.sh outside the path subtree should not be vendored (stat err = %v)", err)
	}
}

func TestAdd_updateKeepsPath(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--path", subPath)

	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV2)

	dep := loadManifestFor(t, dir).Deps[0]
	if dep.Path != subPath || dep.Version != tagV2 {
		t.Errorf("dep = %+v, want path %q preserved at %s", dep, subPath, tagV2)
	}
}

func TestAdd_pathInvalid(t *testing.T) {
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	_, err := runGraft(t, "add", ghRepo+"@v1.0.0", "--path", "../up")
	wantExit(t, err, clierr.CodeConfig)
}

func TestAdd_nameFlag(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	out := mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", nameTools)

	if !strings.Contains(out, "✓ added tools v1.0.0") {
		t.Errorf("output = %q", out)
	}

	if got := loadManifestFor(t, dir).Deps[0].Name; got != nameTools {
		t.Errorf("dep name = %q, want %q", got, nameTools)
	}

	if got := readProjectFile(t, dir, "deps/tools/run.sh"); got != contentV1 {
		t.Errorf("run.sh = %q", got)
	}
}

func TestAdd_repoFormKeepsCustomName(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", nameTools)

	// Repo form without --name matches the single entry by repo and keeps
	// its custom name (spec §4.2).
	out := mustRunGraft(t, "add", f.repo.URL()+"@"+tagV2)

	if !strings.Contains(out, "✓ updated tools to v2.0.0") {
		t.Errorf("output = %q", out)
	}

	m := loadManifestFor(t, dir)
	if len(m.Deps) != 1 || m.Deps[0].Name != nameTools {
		t.Errorf("manifest deps = %+v", m.Deps)
	}
}

func TestAdd_sameRepoTwoEntries(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", "whole")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", "subdir", "--path", subPath)

	// Each entry updates independently via repo URL + --name.
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV2, "--name", "whole")

	m := loadManifestFor(t, dir)
	whole := m.FindDep("whole")
	subdir := m.FindDep("subdir")

	if len(m.Deps) != 2 || whole == nil || subdir == nil || whole.Version != tagV2 || subdir.Version != tagV1 {
		t.Errorf("manifest deps = %+v", m.Deps)
	}

	if got := readProjectFile(t, dir, "deps/whole/run.sh"); got != contentV2 {
		t.Errorf("whole/run.sh = %q", got)
	}

	if got := readProjectFile(t, dir, "deps/subdir/lib.sh"); got != contentLib {
		t.Errorf("subdir/lib.sh = %q", got)
	}

	// Repo form without --name is ambiguous when multiple entries share the repo: error.
	_, err := runGraft(t, "add", f.repo.URL()+"@"+tagV2)
	wantExit(t, err, clierr.CodeConfig)

	if msg := err.Error(); !strings.Contains(msg, "subdir, whole") {
		t.Errorf("error %q should list the matching names", msg)
	}
}

func TestAdd_derivedNameCollision(t *testing.T) {
	f1, f2 := newFixtureRemote(t), newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f1.repo.URL()+"@"+tagV1)

	// Both fixture remotes derive the name "remote": adding the second repo
	// must fail instead of silently re-pointing the first entry.
	_, err := runGraft(t, "add", f2.repo.URL()+"@"+tagV1)
	wantExit(t, err, clierr.CodeConfig)

	mustRunGraft(t, "add", f2.repo.URL()+"@"+tagV1, "--name", "other")

	m := loadManifestFor(t, dir)

	if len(m.Deps) != 2 || m.FindDep("other") == nil || m.FindDep(depRemote) == nil {
		t.Errorf("manifest deps = %+v", m.Deps)
	}
}

func TestAdd_nameInvalid(t *testing.T) {
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	_, err := runGraft(t, "add", ghRepo+"@v1.0.0", "--name", "bad name")
	wantExit(t, err, clierr.CodeConfig)
}

func TestAdd_pathLikeName(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", "nested/remote")

	if got := loadManifestFor(t, dir).Deps[0].Name; got != "nested/remote" {
		t.Errorf("manifest name = %q, want %q", got, "nested/remote")
	}

	lf := loadLockFor(t, dir)
	if got := lf.Dest(lf.Deps[0]); got != "deps/nested/remote" {
		t.Errorf("locked dest = %q", got)
	}

	if got := readProjectFile(t, dir, "deps/nested/remote/run.sh"); got != contentV1 {
		t.Errorf("run.sh = %q", got)
	}

	// Updating by repo URL without --name keeps the path-like name.
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV2)

	if got := readProjectFile(t, dir, "deps/nested/remote/run.sh"); got != contentV2 {
		t.Errorf("run.sh after update = %q", got)
	}
}

func TestAdd_nameInvalidPath(t *testing.T) {
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	// Rejected by flag validation before any network access — the repo is
	// never contacted.
	_, err := runGraft(t, "add", ghRepo+"@v1.0.0", "--name", "../escape")
	wantExit(t, err, clierr.CodeConfig)
}

// TestTargetDep_canonicalMatch pins that repo-form entry identification (§4.2)
// compares the canonical <host>/<org>/<repo> form (§10.8): the same remote
// written as SSH, with a .git suffix, or with an https:// scheme all match an
// existing entry, while a genuinely different repo does not. It exercises
// targetDep directly so no network access is needed.
func TestTargetDep_canonicalMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		stored    string // the existing entry's repo, in some spelling
		wantMatch bool
	}{
		{"ssh form", sshRepo, true},
		{"dot-git suffix", ghRepo + ".git", true},
		{"https scheme", "https://" + ghRepo, true},
		{"different repo", "github.com/org/other", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &config.Manifest{
				Vendor: "deps",
				Deps:   []config.Dep{{Name: "existing", Repo: tt.stored, Version: tagV1}},
			}

			dep, _, err := targetDep(m, ghRepo, addOpts{})
			if err != nil {
				t.Fatalf("targetDep returned error: %v", err)
			}

			switch {
			case tt.wantMatch && (dep == nil || dep.Name != "existing"):
				t.Errorf("base %q should match stored %q, got dep %+v", ghRepo, tt.stored, dep)
			case !tt.wantMatch && dep != nil:
				t.Errorf("base %q should not match stored %q, got dep %+v", ghRepo, tt.stored, dep)
			}
		})
	}
}

func TestAdd_repointWithName(t *testing.T) {
	f1, f2 := newFixtureRemote(t), newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f1.repo.URL()+"@"+tagV1, "--name", nameTools)

	// Repo form + --name naming an existing entry is a deliberate re-point: the
	// nameTools entry now tracks a different repo (spec §4.2).
	mustRunGraft(t, "add", f2.repo.URL()+"@"+tagV1, "--name", nameTools)

	m := loadManifestFor(t, dir)
	if len(m.Deps) != 1 || m.Deps[0].Name != nameTools || m.Deps[0].Repo != f2.repo.URL() {
		t.Errorf("manifest deps = %+v, want a single tools entry pointing at f2", m.Deps)
	}

	if got := loadLockFor(t, dir).Deps[0].Commit; got != f2.v1 {
		t.Errorf("locked commit = %q, want f2 %q", got, f2.v1)
	}
}

func TestAdd_renameViaRemoveAdd(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	// Renaming is a two-step operation: remove the old entry, add with the new name.
	// The remove prunes the old vendor dir; the add installs at the new location.
	mustRunGraft(t, "remove", depRemote)
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", nameTools)

	m := loadManifestFor(t, dir)
	if len(m.Deps) != 1 || m.Deps[0].Name != nameTools {
		t.Errorf("manifest deps = %+v, want a single tools entry", m.Deps)
	}

	if got := readProjectFile(t, dir, "deps/tools/run.sh"); got != contentV1 {
		t.Errorf("deps/tools/run.sh = %q", got)
	}

	if _, err := os.Stat(filepath.Join(dir, "deps", "remote")); !os.IsNotExist(err) {
		t.Errorf("old dest deps/remote should be pruned (stat err = %v)", err)
	}
}

func TestAdd_nestedDestConflict(t *testing.T) {
	f1, f2 := newFixtureRemote(t), newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f1.repo.URL()+"@"+tagV1, "--name", "foo")

	// "foo/bar" installs at deps/foo/bar, nested under deps/foo — conflict.
	_, err := runGraft(t, "add", f2.repo.URL()+"@"+tagV1, "--name", "foo/bar")
	wantExit(t, err, clierr.CodeConfig)

	m := loadManifestFor(t, dir)
	if len(m.Deps) != 1 || m.Deps[0].Name != "foo" {
		t.Errorf("manifest should be unchanged: %+v", m.Deps)
	}
}
