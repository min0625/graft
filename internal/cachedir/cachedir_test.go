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
