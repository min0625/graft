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

const (
	windowsGOOS = "windows"
	fileATxt    = "a.txt"
)

// goldenHash is the spec §3.2 hash of the fixture tree written by
// writeFixtureTree when no exec bits are set:
// sha256(sort(sha256("a.txt\n\x00hello\n"), sha256("sub/b.bin\n\x00\x00\x01\x02"))).
// The constant pins the algorithm (including exec_byte) so the same tree
// hashes identically on every platform CI runs on.
const goldenHash = "sha256:0e9bfbdbf487c82ca48768e3e29affe15563b74f3208a33e0fcab350df754e66"

// goldenHashAExec is the hash of the same fixture tree when a.txt is marked
// executable via the .graft-execbits metadata file.
const goldenHashAExec = "sha256:e9b48ed672b49eef04fc399c8f88c816f93dc0e85c2c3305594773f7a314fde2"

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
	writeFile(t, root, fileATxt, []byte("hello\n"))
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

func TestHashTree_ignoresFilesystemModeEmptyDirsAndGit(t *testing.T) {
	t.Parallel()

	root := writeFixtureTree(t)

	// Filesystem exec bits do NOT affect the hash — only the .graft-execbits
	// metadata file does. Empty directories and .git are also excluded.
	if runtime.GOOS != windowsGOOS {
		if err := os.Chmod(filepath.Join(root, fileATxt), 0o755); err != nil { //nolint:gosec // Deliberate mode change.
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
	writeFile(t, root, fileATxt, []byte("tampered\n"))

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

	if err := os.Symlink(fileATxt, filepath.Join(root, "link")); err != nil {
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

// TestHashTree_execBitChangesHash verifies that marking a file as executable
// via WriteExecBits changes the tree hash (spec §3.2 exec_byte).
// spec: REQ-HASH-EXECBIT
func TestHashTree_execBitChangesHash(t *testing.T) {
	t.Parallel()

	// Without exec bit: must match goldenHash.
	root := writeFixtureTree(t)

	noExec, err := hasher.HashTree(root)
	if err != nil {
		t.Fatal(err)
	}

	if noExec != goldenHash {
		t.Fatalf("baseline hash = %s, want %s", noExec, goldenHash)
	}

	// With a.txt marked executable via metadata.
	if err := hasher.WriteExecBits(root, map[string]bool{fileATxt: true}); err != nil {
		t.Fatal(err)
	}

	withExec, err := hasher.HashTree(root)
	if err != nil {
		t.Fatal(err)
	}

	if withExec == noExec {
		t.Error("hash unchanged after marking a.txt as executable")
	}

	if withExec != goldenHashAExec {
		t.Errorf("exec hash = %s, want %s", withExec, goldenHashAExec)
	}
}

// TestHashTree_execBitFromMetadataNotFilesystem verifies that the filesystem
// permission mode has no effect on the hash — only the .graft-execbits
// metadata file determines exec status (spec §3.2).
// spec: REQ-HASH-EXECBIT
func TestHashTree_execBitFromMetadataNotFilesystem(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS {
		t.Skip("chmod has no effect on Windows")
	}

	root := writeFixtureTree(t)

	// Mark a.txt executable via metadata only.
	if err := hasher.WriteExecBits(root, map[string]bool{fileATxt: true}); err != nil {
		t.Fatal(err)
	}

	hashWithMeta, err := hasher.HashTree(root)
	if err != nil {
		t.Fatal(err)
	}

	// Also chmod +x a.txt — filesystem mode must not change the hash.
	if err := os.Chmod(filepath.Join(root, fileATxt), 0o755); err != nil { //nolint:gosec // Deliberate mode change.
		t.Fatal(err)
	}

	hashWithBoth, err := hasher.HashTree(root)
	if err != nil {
		t.Fatal(err)
	}

	if hashWithMeta != hashWithBoth {
		t.Errorf("adding filesystem exec bit changed hash: %s vs %s", hashWithMeta, hashWithBoth)
	}

	// chmod -x should also leave the hash unchanged.
	if err := os.Chmod(filepath.Join(root, fileATxt), 0o600); err != nil {
		t.Fatal(err)
	}

	hashNoFS, err := hasher.HashTree(root)
	if err != nil {
		t.Fatal(err)
	}

	if hashWithMeta != hashNoFS {
		t.Errorf("removing filesystem exec bit changed hash: %s vs %s", hashWithMeta, hashNoFS)
	}
}

// TestWriteExecBits_roundTrip verifies that WriteExecBits and HashTree agree
// regardless of the iteration order of the input map.
// spec: REQ-HASH-EXECBIT
func TestWriteExecBits_roundTrip(t *testing.T) {
	t.Parallel()

	root1 := writeFixtureTree(t)
	if err := hasher.WriteExecBits(root1, map[string]bool{fileATxt: true, "sub/b.bin": false}); err != nil {
		t.Fatal(err)
	}

	h1, err := hasher.HashTree(root1)
	if err != nil {
		t.Fatal(err)
	}

	// Same exec bits, different map insertion order.
	root2 := writeFixtureTree(t)
	if err := hasher.WriteExecBits(root2, map[string]bool{"sub/b.bin": false, fileATxt: true}); err != nil {
		t.Fatal(err)
	}

	h2, err := hasher.HashTree(root2)
	if err != nil {
		t.Fatal(err)
	}

	if h1 != h2 {
		t.Errorf("hash differs between equivalent exec-bit maps: %s vs %s", h1, h2)
	}
}

// TestHashTree_rejectsCaseFoldCollision verifies that two paths that differ
// only in case are rejected with exit 2 (spec §3.2).
// spec: REQ-HASH-CASECOLLIDE
func TestHashTree_rejectsCaseFoldCollision(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS || runtime.GOOS == "darwin" {
		t.Skip("case-distinct files cannot be created on case-insensitive filesystems (Windows, macOS APFS)")
	}

	root := t.TempDir()
	writeFile(t, root, "Foo.txt", []byte("upper"))
	writeFile(t, root, "foo.txt", []byte("lower"))

	_, err := hasher.HashTree(root)
	wantConfigErr(t, err)
}

// TestHashTree_rejectsUnicodeNormCollision verifies that two paths that are
// identical after NFC normalization are rejected with exit 2 (spec §3.2).
// spec: REQ-HASH-UNICODECOLLIDE
func TestHashTree_rejectsUnicodeNormCollision(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == windowsGOOS || runtime.GOOS == "darwin" {
		t.Skip(
			"NFD/NFC-distinct filenames cannot be created on filesystems that normalize Unicode (Windows, macOS APFS/HFS+)",
		)
	}

	root := t.TempDir()

	// "é" written as NFD (e + U+0301 combining acute) vs NFC (U+00E9 precomposed).
	nfd := "é.txt" // NFD form
	nfc := "é.txt"  // NFC form — same glyph, different bytes

	writeFile(t, root, nfd, []byte("nfd"))
	writeFile(t, root, nfc, []byte("nfc"))

	_, err := hasher.HashTree(root)
	wantConfigErr(t, err)
}
