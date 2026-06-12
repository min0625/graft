// Copyright 2026 The Graft Authors

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
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
vendor = "deps"

[[deps]]
name    = "scripts"
repo    = "github.com/org/scripts"
version = "v1.2.0"

[[deps]]
name    = "proto"
repo    = "github.com/org/mono"
version = "v0.8.1"
path    = "proto/"
dest    = "third_party/proto"
`))
	if err != nil {
		t.Fatal(err)
	}

	if m.Vendor != "deps" {
		t.Errorf("Vendor = %q, want %q", m.Vendor, "deps")
	}

	if len(m.Deps) != 2 {
		t.Fatalf("len(Deps) = %d, want 2", len(m.Deps))
	}

	if got := m.Deps[1].Path; got != "proto" {
		t.Errorf("trailing slash not normalized: Path = %q, want %q", got, "proto")
	}

	if got := m.ResolvedDest(m.Deps[0]); got != "deps/scripts" {
		t.Errorf("default dest = %q, want %q", got, "deps/scripts")
	}

	if got := m.ResolvedDest(m.Deps[1]); got != "third_party/proto" {
		t.Errorf("explicit dest = %q, want %q", got, "third_party/proto")
	}
}

func TestLoad_errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest string
	}{
		{"missing vendor", `
[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"vendor is dot", `vendor = "."`},
		{"vendor is absolute", `vendor = "/abs"`},
		{"vendor escapes repo", `vendor = "../deps"`},
		{"vendor with backslash", `vendor = "deps\\sub"`},
		{"unknown key", `
vendor = "deps"
unknown = true
`},
		{"invalid toml", `vendor = `},
		{"invalid name", `
vendor = "deps"

[[deps]]
name    = "a/b"
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"dot name", `
vendor = "deps"

[[deps]]
name    = ".."
repo    = "github.com/org/a"
version = "v1.0.0"
`},
		{"duplicate name", `
vendor = "deps"

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
vendor = "deps"

[[deps]]
name    = "a"
version = "v1.0.0"
`},
		{"missing version", `
vendor = "deps"

[[deps]]
name = "a"
repo = "github.com/org/a"
`},
		{"absolute dest", `
vendor = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
dest    = "/tmp/a"
`},
		{"dest with dotdot", `
vendor = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
dest    = "ok/../../escape"
`},
		{"path with dotdot", `
vendor = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
path    = "../up"
`},
		{"dest equals vendor", `
vendor = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
dest    = "deps"
`},
		{"dest contains vendor", `
vendor = "deps/sub"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
dest    = "deps"
`},
		{"same dest", `
vendor = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"
dest    = "shared"

[[deps]]
name    = "b"
repo    = "github.com/org/b"
version = "v1.0.0"
dest    = "shared"
`},
		{"nested dest", `
vendor = "deps"

[[deps]]
name    = "a"
repo    = "github.com/org/a"
version = "v1.0.0"

[[deps]]
name    = "b"
repo    = "github.com/org/b"
version = "v1.0.0"
dest    = "deps/a/nested"
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
		Vendor: "deps",
		Deps: []config.Dep{
			{Name: "a", Repo: "github.com/org/a", Version: "v1.0.0"},
			{Name: "b", Repo: "github.com/org/b", Version: "v2.0.0", Path: "sub", Dest: "third_party/b"},
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

	if got.Vendor != m.Vendor || len(got.Deps) != len(m.Deps) {
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

	m := &config.Manifest{Vendor: ""}

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

		if err := os.WriteFile(filepath.Join(root, config.Filename), []byte(`vendor = "deps"`), 0o600); err != nil {
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
		if err := os.WriteFile(filepath.Join(root, config.Filename), []byte(`vendor = "deps"`), 0o600); err != nil {
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

		if err := os.WriteFile(filepath.Join(gitRepo, config.Filename), []byte(`vendor = "deps"`), 0o600); err != nil {
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
