// Copyright 2026 The Graft Authors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/cachedir"
)

// linkTarget returns the symlink/junction target of the dep dest, failing the
// test if it is not a link.
func linkTarget(t *testing.T, dir string) string {
	t.Helper()

	target, err := os.Readlink(filepath.Join(dir, "deps", "scripts"))
	if err != nil {
		t.Fatalf("dest is not a link: %v", err)
	}

	return target
}

func TestApply_linkMode(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	t.Setenv("GRAFT_LINK_MODE", "symlink")
	mustRunGraft(t, "apply")

	// The dest is a directory symlink into the store, and content resolves.
	if !strings.Contains(linkTarget(t, dir), filepath.Join("store", "sha256")) {
		t.Errorf("link target = %q, want a store path", linkTarget(t, dir))
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("run.sh through link = %q", got)
	}

	if out := mustRunGraft(t, "status"); !strings.Contains(out, "✓ scripts") {
		t.Errorf("status after link apply = %q", out)
	}
}

func TestApply_invalidLinkMode(t *testing.T) {
	newProjectDir(t)
	t.Setenv("GRAFT_LINK_MODE", "bogus")

	_, stderr, exitCode := runGraftStreams(t, "apply")
	if exitCode != 2 {
		t.Errorf("exit code = %d, want 2", exitCode)
	}

	if !strings.Contains(stderr, `invalid GRAFT_LINK_MODE "bogus"`) {
		t.Errorf("stderr = %q, want invalid-link-mode error", stderr)
	}
}

// TestApply_modeSwitchRewrites covers spec §5.6: a dest materialized in one
// mode is treated as drift and rewritten when the other mode is applied.
func TestApply_modeSwitchRewrites(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")

	// copy → link
	mustRunGraft(t, "apply")
	t.Setenv("GRAFT_LINK_MODE", "symlink")
	mustRunGraft(t, "apply")

	if _, err := os.Readlink(filepath.Join(dir, "deps", "scripts")); err != nil {
		t.Fatalf("copy dest was not rewritten to a link: %v", err)
	}

	// link → copy
	t.Setenv("GRAFT_LINK_MODE", "copy")
	mustRunGraft(t, "apply")

	dest := filepath.Join(dir, "deps", "scripts")
	if _, err := os.Readlink(dest); err == nil {
		t.Fatal("link dest was not rewritten to a copy")
	}

	info, err := os.Stat(dest)
	if err != nil || !info.IsDir() {
		t.Fatalf("copy dest is not a directory: %v", err)
	}

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("run.sh after switch back to copy = %q", got)
	}
}

// TestApply_linkRestoredAfterCacheWipe covers the clean→missing→re-apply loop
// of spec §4.6: a removed store entry leaves the link dangling (status
// missing), and apply re-materializes it. The cache is always safe to delete,
// so wiping it is just an os.RemoveAll of the cache directory.
func TestApply_linkRestoredAfterCacheWipe(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1))
	mustRunGraft(t, "lock")
	t.Setenv("GRAFT_LINK_MODE", "symlink")
	mustRunGraft(t, "apply")

	// Wipe the cache: the store entry the link points at is now gone.
	//nolint:gosec // Cache dir is set by newProjectDir to a t.TempDir() path.
	if err := os.RemoveAll(os.Getenv(cachedir.EnvOverride)); err != nil {
		t.Fatal(err)
	}

	if out, _ := runGraft(t, "status"); !strings.Contains(out, "✗ scripts") ||
		!strings.Contains(out, "missing") {
		t.Errorf("status after clean should show missing: %q", out)
	}

	// apply re-fetches, repopulates the store, and re-links.
	mustRunGraft(t, "apply")

	if got := readProjectFile(t, dir, runShPath); got != contentV1 {
		t.Errorf("run.sh after restore = %q", got)
	}

	if out := mustRunGraft(t, "status"); !strings.Contains(out, "✓ scripts") {
		t.Errorf("status after restore = %q", out)
	}
}
