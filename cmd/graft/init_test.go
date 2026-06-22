// Copyright 2026 The Graft Authors

package main

import (
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
)

func TestInit_createsManifest(t *testing.T) {
	dir := newProjectDir(t)

	out := mustRunGraft(t, "init", "deps")

	if want := "✓ created graft.toml (dir: deps)\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}

	want := config.Header + "dir = \"deps\"\n"
	if got := readProjectFile(t, dir, "graft.toml"); got != want {
		t.Errorf("graft.toml = %q", got)
	}
}

func TestInit_defaultDir(t *testing.T) {
	dir := newProjectDir(t)

	out := mustRunGraft(t, "init")

	if want := "✓ created graft.toml (dir: deps)\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}

	want := config.Header + "dir = \"deps\"\n"
	if got := readProjectFile(t, dir, "graft.toml"); got != want {
		t.Errorf("graft.toml = %q", got)
	}
}

func TestInit_alreadyExists(t *testing.T) {
	dir := newProjectDir(t)
	writeProjectFile(t, dir, "graft.toml", `dir = "deps"`)

	_, err := runGraft(t, "init", "deps")
	wantExit(t, err, clierr.CodeConfig)

	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v", err)
	}
}

func TestInit_invalidDir(t *testing.T) {
	newProjectDir(t)

	for _, vendor := range []string{"", ".", "../up", "/abs"} {
		_, err := runGraft(t, "init", vendor)
		wantExit(t, err, clierr.CodeConfig)

		// All invalid values share the "invalid dir" wording; none should tell
		// the user to run `graft init` while they are running it.
		if msg := err.Error(); !strings.Contains(msg, "invalid dir") ||
			strings.Contains(msg, "run `graft init`") {
			t.Errorf("init %q: misleading error %q", vendor, msg)
		}
	}
}
