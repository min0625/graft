// Copyright 2026 The Graft Authors

package main

import (
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

	if want := "✓ installed scripts v1.0.0\n"; out != want {
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

	if !strings.Contains(out, "✓ installed scripts v1.0.0") {
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
