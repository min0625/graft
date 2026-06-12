// Copyright 2026 The Graft Authors

package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
)

const (
	ghRepo  = "github.com/org/repo"
	sshRepo = "git@github.com:org/repo.git"
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

	out := mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", depScripts)

	if !strings.Contains(out, "✓ added scripts v1.0.0 ("+f.v1[:7]+")") {
		t.Errorf("output = %q", out)
	}

	m := loadManifestFor(t, dir)
	if len(m.Deps) != 1 || m.Deps[0].Version != tagV1 || m.Deps[0].Name != depScripts {
		t.Errorf("manifest deps = %+v", m.Deps)
	}

	lf := loadLockFor(t, dir)
	if lf.Deps[0].Commit != f.v1 {
		t.Errorf("locked commit = %q, want %q", lf.Deps[0].Commit, f.v1)
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("installed run.sh = %q", got)
	}
}

func TestAdd_newDepByBranch(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	mustRunGraft(t, "add", f.repo.URL()+"@dev", "--name", depScripts)

	m := loadManifestFor(t, dir)
	if v := m.Deps[0].Version; !pseudoVersionRe.MatchString(v) {
		t.Errorf("version = %q, want a pseudo-version", v)
	}

	if got := loadLockFor(t, dir).Deps[0].Commit; got != f.dev {
		t.Errorf("locked commit = %q, want branch tip %q", got, f.dev)
	}

	if got := readProjectFile(t, dir, "deps/scripts/dev.txt"); got != "dev\n" {
		t.Errorf("dev.txt = %q", got)
	}
}

func TestAdd_newDepByPartialSHA(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	mustRunGraft(t, "add", f.repo.URL()+"@"+f.v1[:10], "--name", depScripts)

	m := loadManifestFor(t, dir)
	if v := m.Deps[0].Version; !pseudoVersionRe.MatchString(v) {
		t.Errorf("version = %q, want a pseudo-version", v)
	}

	if got := loadLockFor(t, dir).Deps[0].Commit; got != f.v1 {
		t.Errorf("locked commit = %q, want %q", got, f.v1)
	}
}

func TestAdd_updateByName(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", depScripts)

	out := mustRunGraft(t, "add", "scripts@v2.0.0")

	if !strings.Contains(out, "✓ updated scripts to v2.0.0 ("+f.v2[:7]+")") {
		t.Errorf("output = %q", out)
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV2 {
		t.Errorf("run.sh = %q, want the v2 content", got)
	}
}

func TestAdd_sameCommitIsNoop(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1, "--name", depScripts)

	before := readProjectFile(t, dir, "graft.lock")

	// Re-adding the same commit by SHA keeps the stored tag version.
	out := mustRunGraft(t, "add", "scripts@"+f.v1)

	if !strings.Contains(out, "✓ scripts already at "+f.v1[:7]+" (v1.0.0)") {
		t.Errorf("output = %q", out)
	}

	if loadManifestFor(t, dir).Deps[0].Version != tagV1 {
		t.Error("no-op add rewrote the stored version")
	}

	if after := readProjectFile(t, dir, "graft.lock"); after != before {
		t.Errorf("no-op add changed the lockfile:\n%s\n---\n%s", before, after)
	}
}

func TestAdd_unknownName(t *testing.T) {
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	_, err := runGraft(t, "add", "nope@v1.0.0")
	wantExit(t, err, clierr.CodeConfig)
}

func TestAdd_missingRef(t *testing.T) {
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	_, err := runGraft(t, "add", ghRepo)
	wantExit(t, err, clierr.CodeGeneral)
}

func TestAdd_resolvesPreexistingDrift(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))

	// No lockfile yet — add must not fail on the toml ↔ lock mismatch.
	other := newFixtureRemote(t)
	mustRunGraft(t, "add", other.repo.URL()+"@"+tagV1, "--name", "other")

	lf := loadLockFor(t, dir)
	if len(lf.Deps) != 2 {
		t.Fatalf("lock deps = %+v, want both deps locked", lf.Deps)
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("pre-existing dep not installed: %q", got)
	}

	if got := readProjectFile(t, dir, "deps/other/run.sh"); got != contentV1 {
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
