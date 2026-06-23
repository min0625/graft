// Copyright 2026 The Graft Authors

package cachedir_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/min0625/graft/internal/cachedir"
)

func TestDir_envOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom-cache")
	t.Setenv(cachedir.EnvOverride, want)

	got, err := cachedir.Dir()
	if err != nil {
		t.Fatal(err)
	}

	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestDir_osConvention(t *testing.T) {
	t.Setenv(cachedir.EnvOverride, "")

	got, err := cachedir.Dir()
	if err != nil {
		t.Fatal(err)
	}

	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(base, "graft")
	if runtime.GOOS == "windows" {
		want = filepath.Join(base, "graft", "cache")
	}

	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestSubdirs_lazilyCreate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	t.Setenv(cachedir.EnvOverride, root)

	cases := []struct {
		name string
		fn   func() (string, error)
		sub  string
	}{
		{"Repos", cachedir.Repos, cachedir.ReposSubdir},
		{"Store", cachedir.Store, cachedir.StoreSubdir},
		{"Links", cachedir.Links, cachedir.LinksSubdir},
		{"Tmp", cachedir.Tmp, cachedir.TmpSubdir},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn()
			if err != nil {
				t.Fatal(err)
			}

			want := filepath.Join(root, tc.sub)
			if got != want {
				t.Errorf("%s() = %q, want %q", tc.name, got, want)
			}

			info, err := os.Stat(got)
			if err != nil {
				t.Fatalf("%s not created: %v", tc.name, err)
			}

			if !info.IsDir() {
				t.Errorf("%s is not a directory", got)
			}
		})
	}
}

func TestSubdirs_idempotent(t *testing.T) {
	t.Setenv(cachedir.EnvOverride, filepath.Join(t.TempDir(), "cache"))

	for range 2 {
		if _, err := cachedir.Store(); err != nil {
			t.Fatalf("Store() must succeed when the directory already exists: %v", err)
		}
	}
}

func TestHasTag(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	t.Setenv(cachedir.EnvOverride, root)

	// Before any subdir is created, no tag.
	if cachedir.HasTag(root) {
		t.Fatal("HasTag should be false before cache is initialised")
	}

	if _, err := cachedir.Store(); err != nil {
		t.Fatal(err)
	}

	// After first ensureSubdir, tag must exist.
	if !cachedir.HasTag(root) {
		t.Fatal("HasTag should be true after cache is initialised")
	}

	// Idempotent — second call must not error.
	if _, err := cachedir.Store(); err != nil {
		t.Fatalf("Store() must succeed when CACHEDIR.TAG already exists: %v", err)
	}
}

func TestDirSize_countsFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files := map[string]string{
		"a.txt":    "hello",     // 5 bytes
		"sub/b.sh": "#!/bin/sh", // 9 bytes
	}

	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(content), 0o644); err != nil { //nolint:gosec // Test fixture.
			t.Fatal(err)
		}
	}

	got, err := cachedir.DirSize(root)
	if err != nil {
		t.Fatal(err)
	}

	want := int64(5 + 9)
	if got != want {
		t.Errorf("DirSize = %d, want %d", got, want)
	}
}

func TestDirSize_nonExistentDir(t *testing.T) {
	t.Parallel()

	got, err := cachedir.DirSize(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatal(err)
	}

	if got != 0 {
		t.Errorf("DirSize of nonexistent dir = %d, want 0", got)
	}
}
