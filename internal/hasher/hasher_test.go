// Copyright 2026 The Graft Authors

package hasher_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/hasher"
)

const windowsGOOS = "windows"

// goldenHash is the spec §3.2 hash of the fixture tree written by
// writeFixtureTree: sha256(sort(sha256("a.txt\nhello\n"),
// sha256("sub/b.bin\n\x00\x01\x02"))). The constant pins the algorithm so the
// same tree must hash identically on every platform CI runs on.
const goldenHash = "sha256:f801afc2dc0e34f2dd8153d362a283d1c935d0ddc8a0193baa10636cdba7d227"

func writeFile(t *testing.T, root, rel string, content []byte) {
	t.Helper()

	full := filepath.Join(root, filepath.FromSlash(rel))

	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(full, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFixtureTree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, "a.txt", []byte("hello\n"))
	writeFile(t, root, "sub/b.bin", []byte{0x00, 0x01, 0x02})

	return root
}

func wantConfigErr(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("want exit-2 error, got nil")
	}

	if got := clierr.ExitCode(err); got != int(clierr.CodeConfig) {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeConfig, err)
	}
}

func TestHashTree_golden(t *testing.T) {
	t.Parallel()

	got, err := hasher.HashTree(writeFixtureTree(t))
	if err != nil {
		t.Fatal(err)
	}

	if got != goldenHash {
		t.Errorf("HashTree = %s, want %s", got, goldenHash)
	}
}

func TestHashTree_ignoresModesEmptyDirsAndGit(t *testing.T) {
	t.Parallel()

	root := writeFixtureTree(t)

	// An executable bit, an empty directory, and a .git directory must not
	// change the hash.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(root, "a.txt"), 0o755); err != nil { //nolint:gosec // Deliberate mode change.
			t.Fatal(err)
		}
	}

	if err := os.MkdirAll(filepath.Join(root, "empty", "dirs"), 0o750); err != nil {
		t.Fatal(err)
	}

	writeFile(t, root, ".git/HEAD", []byte("ref: refs/heads/main\n"))

	got, err := hasher.HashTree(root)
	if err != nil {
		t.Fatal(err)
	}

	if got != goldenHash {
		t.Errorf("HashTree = %s, want %s", got, goldenHash)
	}
}

func TestHashTree_contentChangesHash(t *testing.T) {
	t.Parallel()

	root := writeFixtureTree(t)
	writeFile(t, root, "a.txt", []byte("tampered\n"))

	got, err := hasher.HashTree(root)
	if err != nil {
		t.Fatal(err)
	}

	if got == goldenHash {
		t.Error("hash unchanged after content edit")
	}
}

func TestHashTree_pathIsPartOfHash(t *testing.T) {
	t.Parallel()

	a := t.TempDir()
	writeFile(t, a, "x.txt", []byte("same"))

	b := t.TempDir()
	writeFile(t, b, "y.txt", []byte("same"))

	ha, err := hasher.HashTree(a)
	if err != nil {
		t.Fatal(err)
	}

	hb, err := hasher.HashTree(b)
	if err != nil {
		t.Fatal(err)
	}

	if ha == hb {
		t.Error("trees with identical content but different paths must hash differently")
	}
}

func TestHashTree_emptyTree(t *testing.T) {
	t.Parallel()

	_, err := hasher.HashTree(t.TempDir())
	wantConfigErr(t, err)
}

func TestHashTree_rejectsSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS {
		t.Skip("symlink creation needs privileges on Windows")
	}

	root := writeFixtureTree(t)

	if err := os.Symlink("a.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	_, err := hasher.HashTree(root)
	wantConfigErr(t, err)
}

func TestHashTree_rejectsUnportablePaths(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS {
		t.Skip("these file names cannot be created on Windows")
	}

	tests := []struct {
		name, file string
	}{
		{"newline", "a\nb.txt"},
		{"invalid windows char", "a<b.txt"},
		{"reserved name", "NUL"},
		{"reserved name with extension", "con.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeFile(t, root, tt.file, []byte("x"))

			_, err := hasher.HashTree(root)
			wantConfigErr(t, err)
		})
	}
}
