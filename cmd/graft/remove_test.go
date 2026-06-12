// Copyright 2026 The Graft Authors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
)

func TestRemove_removesDepAndVendor(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)
	mustRunGraft(t, "init", "deps")
	mustRunGraft(t, "add", f.repo.URL()+"@"+tagV1)

	if _, err := os.Stat(filepath.Join(dir, "deps", depRemote)); err != nil {
		t.Fatalf("vendor dir missing before remove: %v", err)
	}

	out := mustRunGraft(t, "remove", depRemote)

	if _, err := os.Stat(filepath.Join(dir, "deps", depRemote)); !os.IsNotExist(err) {
		t.Error("vendor dir still exists after remove")
	}

	if !strings.Contains(out, "✓ removed "+depRemote) {
		t.Errorf("output = %q", out)
	}

	m := loadManifestFor(t, dir)
	for _, dep := range m.Deps {
		if dep.Name == depRemote {
			t.Error("dep still in manifest after remove")
		}
	}

	lf := loadLockFor(t, dir)
	for _, ld := range lf.Deps {
		if ld.Name == depRemote {
			t.Error("dep still in lockfile after remove")
		}
	}
}

func TestRemove_nameNotFound(t *testing.T) {
	newProjectDir(t)
	mustRunGraft(t, "init", "deps")

	_, err := runGraft(t, "remove", "nonexistent")
	wantExit(t, err, clierr.CodeConfig)
}
