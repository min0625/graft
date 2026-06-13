// Copyright 2026 The Graft Authors

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/min0625/graft/internal/clierr"
)

// wantStatusRow asserts that out contains a status line for name ending in
// state, e.g. "✓ remote  1a2b3c4 (v1.0.0)  ok".
func wantStatusRow(t *testing.T, out, mark, name, state string) {
	t.Helper()

	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(mark+" "+name) + `\s.*\s` + regexp.QuoteMeta(state) + `\s*$`)
	if !re.MatchString(out) {
		t.Errorf("no %q row with state %q in output:\n%s", name, state, out)
	}
}

func TestStatus_ok(t *testing.T) {
	f := newFixtureRemote(t)
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	out := mustRunGraft(t, "status")

	wantStatusRow(t, out, "✓", depRemote, "ok")
}

func TestStatus_missing(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	// Remove the vendor dir to simulate missing.
	if err := os.RemoveAll(filepath.Join(dir, "deps", depRemote)); err != nil {
		t.Fatal(err)
	}

	out, err := runGraft(t, "status")
	wantExit(t, err, clierr.CodeGeneral)

	wantStatusRow(t, out, "✗", depRemote, "missing")
}

func TestStatus_modified(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	// Modify a file in the vendor directory.
	writeProjectFile(t, dir, "deps/remote/run.sh", "tampered\n")

	out, err := runGraft(t, "status")
	wantExit(t, err, clierr.CodeGeneral)

	wantStatusRow(t, out, "✗", depRemote, "modified")
}

func TestStatus_outOfSync(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	// Change the version in graft.toml without re-running lock — out of sync.
	m := loadManifestFor(t, dir)

	m.Deps[0].Version = tagV2
	if err := m.Write(filepath.Join(dir, "graft.toml")); err != nil {
		t.Fatal(err)
	}

	out, err := runGraft(t, "status")
	wantExit(t, err, clierr.CodeGeneral)

	wantStatusRow(t, out, "✗", depRemote, "out of sync")
}

func TestStatus_noLockfile(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	// Delete the lockfile.
	if err := os.Remove(filepath.Join(dir, "graft.lock")); err != nil {
		t.Fatal(err)
	}

	out, err := runGraft(t, "status")
	wantExit(t, err, clierr.CodeGeneral)

	wantStatusRow(t, out, "✗", depRemote, "out of sync")
}

func TestStatus_extra(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	// Write an extra directory into the vendor dir that graft doesn't own.
	extraDir := filepath.Join(dir, "deps", "extra-lib")
	if err := os.MkdirAll(extraDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(extraDir, "file.txt"), []byte("extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runGraft(t, "status")
	wantExit(t, err, clierr.CodeGeneral)

	wantStatusRow(t, out, "✗", "deps/extra-lib", "extra")
}

func TestStatus_exitZeroWhenClean(t *testing.T) {
	f := newFixtureRemote(t)
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	// Exit 0 when everything is fine.
	mustRunGraft(t, "status")
}
