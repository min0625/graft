// Copyright 2026 The Graft Authors

// Package hasher computes the spec §3.2 content hash of an installed file
// tree: sha256(sort(sha256(filepath + "\n" + exec_byte + content))), where
// exec_byte is 0x00 for regular files and 0x01 for executable files, and
// paths are relative to the tree root and slash-separated, so the same tree
// hashes identically on every platform.
package hasher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/min0625/graft/internal/clierr"
	"golang.org/x/text/unicode/norm"
)

// Prefix starts every hash value recorded in graft.lock.
const Prefix = "sha256:"

// execBitsFile is the metadata file graft writes at each dep tree root,
// recording which files carry the executable bit. HashTree reads it to
// determine per-file exec status and skips it in the hash walk.
// Repositories must not contain a file with this name at their root.
const execBitsFile = ".graft-execbits"

// invalidWindowsChars are path characters that cannot appear in a file name
// on Windows. The newline is rejected separately to keep the
// "filepath\nexec_byte\ncontent" hash input unambiguous.
const invalidWindowsChars = `<>:"\|?*`

// WriteExecBits records which paths in execBits are executable into
// execBitsFile at dir. It is called by the fetcher after checkout so that
// all downstream consumers (store, vendor, status) can reproduce the same
// exec-bit information without re-querying the git index (spec §3.2).
func WriteExecBits(dir string, execBits map[string]bool) error {
	paths := make([]string, 0, len(execBits))
	for p, exec := range execBits {
		if exec {
			paths = append(paths, p)
		}
	}

	if len(paths) == 0 {
		return nil
	}

	slices.Sort(paths)

	//nolint:gosec // The exec-bits file lives in a tree owned by graft.
	return os.WriteFile(
		filepath.Join(dir, execBitsFile),
		[]byte(strings.Join(paths, "\n")+"\n"),
		0o644,
	)
}

// readExecBits reads execBitsFile from dir and returns the set of executable
// paths. Returns nil if the file is absent (no exec files).
func readExecBits(dir string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(dir, execBitsFile)) //nolint:gosec // path under graft's own tree root.
	if err != nil {
		return nil
	}

	result := make(map[string]bool)

	for line := range strings.Lines(string(data)) {
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			result[line] = true
		}
	}

	return result
}

// HashTree hashes the file tree rooted at root and returns
// "sha256:<64 hex digits>".
//
// Normalization rules (spec §3.2): directories named .git are skipped;
// symlinks and other non-regular files are rejected (exit 2); the exec-bits
// metadata file (.graft-execbits) is read first, then excluded from the hash;
// paths invalid on Windows or containing newlines are rejected (exit 2);
// case-folding and Unicode-normalization path collisions are rejected (exit 2);
// a tree with no files is rejected (exit 2); empty directories do not participate.
func HashTree(root string) (string, error) {
	execBits := readExecBits(root)

	var digests []string

	// seen tracks each path's canonical form (NFC+lowercased) for collision detection.
	seen := make(map[string]string)

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}

			return nil
		}

		if rel == execBitsFile {
			return nil
		}

		if !d.Type().IsRegular() {
			if d.Type()&fs.ModeSymlink != 0 {
				return clierr.New(
					clierr.CodeConfig,
					fmt.Sprintf("symlink %q in the dependency tree", rel),
					"symlinks cannot be installed portably; pass --symlinks=skip to `graft add` to skip them, or — for a dependency already in graft.toml — set `symlinks = \"skip\"` on its entry and re-lock",
				)
			}

			return clierr.New(clierr.CodeConfig,
				fmt.Sprintf("unsupported file type for %q in the dependency tree", rel),
				"only regular files are supported",
			)
		}

		if err := validatePath(rel); err != nil {
			return err
		}

		if err := checkPathCollision(rel, seen); err != nil {
			return err
		}

		digest, err := hashFile(rel, p, execBits[rel])
		if err != nil {
			return err
		}

		digests = append(digests, digest)

		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}

	if len(digests) == 0 {
		return "", clierr.New(clierr.CodeConfig,
			"the dependency tree contains no files",
			"an empty dependency is almost always a mistyped `path` — check the manifest",
		)
	}

	slices.Sort(digests)

	final := sha256.Sum256([]byte(strings.Join(digests, "")))

	return Prefix + hex.EncodeToString(final[:]), nil
}

// RemoveSymlinks deletes all symlinks found under root and returns their relative paths.
// Used by deps with symlinks = "skip" before hashing and storing.
func RemoveSymlinks(root string) ([]string, error) {
	var skipped []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.Type()&fs.ModeSymlink != 0 {
			rel, _ := filepath.Rel(root, p)
			skipped = append(skipped, filepath.ToSlash(rel))

			return os.Remove(p) //nolint:gosec // p is confirmed a symlink by the walk callback, not a traversal risk
		}

		return nil
	})

	return skipped, err
}

// hashFile returns the hex sha256 of rel + "\n" + exec_byte + the file's raw bytes.
// exec_byte is 0x01 for executable files (git mode 100755), 0x00 otherwise.
func hashFile(rel, path string, isExec bool) (string, error) {
	f, err := os.Open(path) //nolint:gosec // The path comes from walking the tree being hashed.
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // Read-only file.

	h := sha256.New()
	h.Write([]byte(rel + "\n"))

	if isExec {
		h.Write([]byte{0x01})
	} else {
		h.Write([]byte{0x00})
	}

	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("read %s: %w", rel, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// windowsReservedNames are file name stems Windows refuses to create.
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// validatePath rejects slash-separated relative paths that cannot be
// represented on every supported platform (spec §3.2).
func validatePath(rel string) error {
	reject := func(reason string) error {
		return clierr.New(clierr.CodeConfig,
			fmt.Sprintf("unsupported file path %q in the dependency tree", rel),
			reason,
		)
	}

	for _, r := range rel {
		if r == '\n' {
			return reject("file paths must not contain newlines")
		}

		if r < 0x20 || r == 0x7f {
			return reject("file paths must not contain control characters")
		}

		if strings.ContainsRune(invalidWindowsChars, r) {
			return reject(
				`file paths must not contain characters that are invalid on Windows (` + invalidWindowsChars + `)`,
			)
		}
	}

	for segment := range strings.SplitSeq(rel, "/") {
		stem, _, _ := strings.Cut(segment, ".")
		if windowsReservedNames[strings.ToUpper(stem)] {
			return reject(fmt.Sprintf("%q is a reserved file name on Windows", segment))
		}
	}

	return nil
}

// checkPathCollision rejects case-folding and Unicode-normalization collisions
// (spec §3.2). seen maps the canonical path to the original path that first
// claimed it.
func checkPathCollision(rel string, seen map[string]string) error {
	canonical := canonicalPath(rel)

	if prev, exists := seen[canonical]; exists {
		return clierr.New(clierr.CodeConfig,
			fmt.Sprintf("path collision in the dependency tree: %q and %q", prev, rel),
			"these paths are identical on case-insensitive or Unicode-normalizing filesystems"+
				" (macOS APFS, Windows NTFS) and would overwrite each other at checkout",
		)
	}

	seen[canonical] = rel

	return nil
}

// canonicalPath returns the collision-detection form of a slash-separated
// path: NFC-normalized then lowercased via Unicode simple case-folding.
func canonicalPath(rel string) string {
	nfc := norm.NFC.String(rel)

	var b strings.Builder

	b.Grow(len(nfc))

	for _, r := range nfc {
		b.WriteRune(unicode.ToLower(unicode.SimpleFold(r)))
	}

	return b.String()
}
