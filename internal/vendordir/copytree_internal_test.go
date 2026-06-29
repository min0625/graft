// Copyright 2026 The Graft Authors

package vendordir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyTree_replicatesSymlink(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := t.TempDir()

	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// ponytail: skip on platforms where symlinks need elevated privileges (e.g. Windows without developer mode)
	if err := os.Symlink("file.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("want symlink at dst/link.txt, got mode %v", fi.Mode())
	}

	got, err := os.Readlink(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if got != "file.txt" {
		t.Errorf("symlink target = %q, want %q", got, "file.txt")
	}
}
