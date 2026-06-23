// Copyright 2026 The Graft Authors

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
	"github.com/min0625/graft/internal/vendordir"
)

func writeManifest(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), config.Filename)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func wantExitCode(t *testing.T, err error, want int) {
	t.Helper()

	if err == nil {
		t.Fatalf("want error with exit code %d, got nil", want)
	}

	if got := clierr.ExitCode(err); got != want {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, want, err)
	}
}

func TestLoad_valid(t *testing.T) {
	t.Parallel()

	m, err := config.Load(writeManifest(t, `
dir = "deps"

[[deps]]
name    = "scripts"
repo    = "github.com/org/scripts"
version = "v1.2.0"

[[deps]]
name    = "proto"
repo    = "github.com/org/mono"
version = "v0.8.1"
subdir  = "proto/"
`))
	if err != nil {
		t.Fatal(err)
	}

	if m.Dir != "deps" {
		t.Errorf("Vendor = %q, want %q", m.Dir, "deps")
	}

	if len(m.Deps) != 2 {
		t.Fatalf("len(Deps) = %d, want 2", len(m.Deps))
	}

	if got := m.Deps[1].Subdir; got != "proto" {
		t.Errorf("trailing slash not normalized: Subdir = %q, want %q", got, "proto")
	}

	if got := m.ResolvedDest(m.Deps[0]); got != "deps/scripts" {
		t.Errorf("resolved dest = %q, want %q", got, "deps/scripts")
	}

	if got := m.ResolvedDest(m.Deps[1]); got != "deps/proto" {
		t.Errorf("resolved dest = %q, want %q", got, "deps/proto")
	}
}

func TestLoad_pathLikeName(t *testing.T) {
	t.Parallel()

	m, err := config.Load(writeManifest(t, `
dir = "deps"

[[deps]]
name    = "tool-a/util"
repo    = "github.com/org/a"
version = "v1.0.0"

[[deps]]
name    = "tool-b/util"
repo    = "github.com/org/b"
version = "v1.0.0"

# A "."-bearing, repo-like name installs at the matching nested path. The
# reserved ".graft" prefix must not reject it: its first segment is "github.com".
[[deps]]
name    = "github.com/min0625/mint"
repo    = "github.com/min0625/mint"
version = "v1.0.0"
`))
	if err != nil {
		t.Fatal(err)
	}

	if got := m.ResolvedDest(m.Deps[0]); got != "deps/tool-a/util" {
		t.Errorf("resolved dest = %q, want %q", got, "deps/tool-a/util")
	}

	if got := m.ResolvedDest(m.Deps[1]); got != "deps/tool-b/util" {
		t.Errorf("resolved dest = %q, want %q", got, "deps/tool-b/util")
	}

	if got := m.ResolvedDest(m.Deps[2]); got != "deps/github.com/min0625/mint" {
		t.Errorf("resolved dest = %q, want %q", got, "deps/github.com/min0625/mint")
	}
}

func TestLoad_errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest string
	}{
		{"missing dir", `
[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"dir is dot", `dir = "."`},
		{"dir is absolute", `dir = "/abs"`},
		{"dir escapes repo", `dir = "../deps"`},
		{"dir with backslash", `dir = "deps\\sub"`},
		// spec: REQ-PATH-GITSEG — a vendor dir of ".git" (or under it) would let
		// the destructive reconcile wipe the real git repository.
		{"dir is dotgit", `dir = ".git"`},
		{"dir under dotgit", `dir = ".git/vendor"`},
		{"dir contains dotgit segment", `dir = "vendor/.git"`},
		{"unknown key", `
dir = "deps"
unknown = true
`},
		{"invalid toml", `dir = `},
		{"invalid name", `
dir = "deps"

[[deps]]
name    = "bad name"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"name with dotdot segment", `
dir = "deps"

[[deps]]
name    = "ok/../escape"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		// spec: REQ-PATH-GITSEG — a ".git" name segment makes the parent repo
		// treat <dir>/.git as a nested gitdir, breaking vendor tracking.
		{"name with dotgit segment", `
dir = "deps"

[[deps]]
name    = "tool/.git"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		// spec: REQ-NAME-STAGING — a name starting with ".graft-tmp" installs
		// into the reconcile staging dir and can never apply.
		{"name is staging dir", `
dir = "deps"

[[deps]]
name    = ".graft-tmp"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"name under staging dir", `
dir = "deps"

[[deps]]
name    = ".graft-tmp/sub"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		// spec: REQ-NAME-STAGING — case-insensitive: ".GRAFT-TMP" collides with
		// the staging dir on case-insensitive filesystems, so reject everywhere.
		{"name is staging dir uppercase", `
dir = "deps"

[[deps]]
name    = ".GRAFT-TMP"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		// spec: REQ-NAME-STAGING — the whole ".graft" prefix is reserved, so any
		// internal-looking name (not just .graft-tmp) is rejected.
		{"name uses reserved prefix", `
dir = "deps"

[[deps]]
name    = ".graft-cache"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"name starts with slash", `
dir = "deps"

[[deps]]
name    = "/absolute"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"dot name", `
dir = "deps"

[[deps]]
name    = ".."
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"duplicate name", `
dir = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"

[[deps]]
name    = "a"
repo    = "github.com/org/other"
version = "v2.0.0"
`},
		{"missing repo", `
dir = "deps"

[[deps]]
name    = "a"
version = "v1.0.0"
`},
		{"missing version", `
dir = "deps"

[[deps]]
name = "a"
repo = "github.com/org/a"
`},
		{"subdir with dotdot", `
dir = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
subdir  = "../up"
`},
		// spec: REQ-PATH-GITSEG
		{"subdir is dotgit", `
dir = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
subdir  = ".git"
`},
		{"nested install paths", `
dir = "deps"

[[deps]]
name    = "foo"
repo    = "github.com/org/a"
version = "v1.0.0"

[[deps]]
name    = "foo/bar"
repo    = "github.com/org/b"
version = "v1.0.0"
`},
		{"invalid symlinks policy", `
dir = "deps"

[[deps]]
name     = "a"
repo     = "github.com/org/a"
version  = "v1.0.0"
symlinks = "follow"
`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(writeManifest(t, tt.manifest))
			wantExitCode(t, err, int(clierr.CodeConfig))
		})
	}
}

func TestWrite_roundTrip(t *testing.T) {
	t.Parallel()

	m := &config.Manifest{
		Dir: "deps",
		Deps: []config.Dep{
			{Name: "a", Repo: "github.com/org/a", Version: "v1.0.0"},
			{Name: "tool-a/util", Repo: "github.com/org/b", Version: "v2.0.0", Subdir: "sub"},
		},
	}

	path := filepath.Join(t.TempDir(), config.Filename)
	if err := m.Write(path); err != nil {
		t.Fatal(err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if got.Dir != m.Dir || len(got.Deps) != len(m.Deps) {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	for i := range m.Deps {
		if got.Deps[i] != m.Deps[i] {
			t.Errorf("Deps[%d] = %+v, want %+v", i, got.Deps[i], m.Deps[i])
		}
	}
}

func TestWrite_invalidManifest(t *testing.T) {
	t.Parallel()

	m := &config.Manifest{Dir: ""}

	err := m.Write(filepath.Join(t.TempDir(), config.Filename))
	wantExitCode(t, err, int(clierr.CodeConfig))
}

func TestFindRoot(t *testing.T) {
	t.Parallel()

	t.Run("walks up to manifest", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		nested := filepath.Join(root, "a", "b")

		if err := os.MkdirAll(nested, 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(root, config.Filename), []byte(`dir = "deps"`), 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := config.FindRoot(nested)
		if err != nil {
			t.Fatal(err)
		}

		if got != root {
			t.Errorf("FindRoot = %q, want %q", got, root)
		}
	})

	t.Run("stops at git boundary", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		gitRepo := filepath.Join(root, "repo")
		nested := filepath.Join(gitRepo, "sub")

		if err := os.MkdirAll(filepath.Join(gitRepo, ".git"), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.MkdirAll(nested, 0o750); err != nil {
			t.Fatal(err)
		}

		// graft.toml above the git boundary must not be found.
		if err := os.WriteFile(filepath.Join(root, config.Filename), []byte(`dir = "deps"`), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := config.FindRoot(nested)
		wantExitCode(t, err, int(clierr.CodeConfig))

		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("error = %v, want the not-found hint", err)
		}
	})

	t.Run("finds manifest at the git boundary itself", func(t *testing.T) {
		t.Parallel()

		gitRepo := t.TempDir()
		nested := filepath.Join(gitRepo, "sub")

		if err := os.MkdirAll(filepath.Join(gitRepo, ".git"), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.MkdirAll(nested, 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(gitRepo, config.Filename), []byte(`dir = "deps"`), 0o600); err != nil {
			t.Fatal(err)
		}

		got, err := config.FindRoot(nested)
		if err != nil {
			t.Fatal(err)
		}

		if got != gitRepo {
			t.Errorf("FindRoot = %q, want %q", got, gitRepo)
		}
	})
}

func TestDefaultName(t *testing.T) {
	t.Parallel()

	const wantRepo = "repo"

	tests := []struct {
		repo, want string
	}{
		{"github.com/org/repo", wantRepo},
		{"github.com/org/repo.git", wantRepo},
		{"https://github.com/org/shared-scripts", "shared-scripts"},
		{"git@github.com:org/repo.git", wantRepo},
		{"git@host.example:repo.git", wantRepo},
		{"github.com/org/repo/", wantRepo},
	}

	for _, tt := range tests {
		if got := config.DefaultName(tt.repo); got != tt.want {
			t.Errorf("DefaultName(%q) = %q, want %q", tt.repo, got, tt.want)
		}
	}
}

// TestValidateName_stagingDirDriftGuard binds config's reserved ".graft" prefix
// to vendordir.StagingDirName: if the staging dir is ever renamed to something
// outside that prefix, ValidateName stops rejecting it and this test fails.
// spec: REQ-NAME-STAGING
func TestValidateName_stagingDirDriftGuard(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		vendordir.StagingDirName,
		vendordir.StagingDirName + "/sub",
		strings.ToUpper(vendordir.StagingDirName),
	} {
		if err := config.ValidateName(name); clierr.ExitCode(err) != int(clierr.CodeConfig) {
			t.Errorf("ValidateName(%q) exit code = %d, want %d", name, clierr.ExitCode(err), clierr.CodeConfig)
		}
	}
}

// TestValidateName_reservedPrefixNeedsHyphen pins the boundary: only ".graft-"
// is reserved, so a bare ".graft" or a ".graft"-but-not-"-" name stays allowed.
func TestValidateName_reservedPrefixNeedsHyphen(t *testing.T) {
	t.Parallel()

	for _, name := range []string{".graft", ".graftish", "graft-tmp"} {
		if err := config.ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
}
