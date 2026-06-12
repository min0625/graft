// Copyright 2026 The Graft Authors

// Package hasher computes the spec §3.2 content hash of an installed file
// tree: sha256(sort(sha256(filepath + "\n" + content))), with paths relative
// to the tree root and slash-separated, so the same tree hashes identically
// on every platform.
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

	"github.com/min0625/graft/internal/clierr"
)

// Prefix starts every hash value recorded in graft.lock.
const Prefix = "sha256:"

// invalidWindowsChars are path characters that cannot appear in a file name
// on Windows. The newline is rejected separately to keep the
// "filepath\ncontent" hash input unambiguous.
const invalidWindowsChars = `<>:"\|?*`

// HashTree hashes the file tree rooted at root and returns
// "sha256:<64 hex digits>".
//
// Normalization rules (spec §3.2): directories named .git are skipped;
// symlinks and other non-regular files are rejected (exit 2); paths invalid
// on Windows or containing newlines are rejected (exit 2); a tree with no
// files at all is rejected (exit 2); empty directories and file modes do not
// participate.
func HashTree(root string) (string, error) {
	var digests []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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

		if !d.Type().IsRegular() {
			return clierr.New(clierr.CodeConfig,
				fmt.Sprintf("unsupported file %q in the dependency tree", rel),
				"symbolic links and other special files cannot be installed portably",
			)
		}

		if err := validatePath(rel); err != nil {
			return err
		}

		digest, err := hashFile(rel, p)
		if err != nil {
			return err
		}

		digests = append(digests, digest)

		return nil
	})
	if err != nil {
		return "", err
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

// hashFile returns the hex sha256 of rel + "\n" + the file's raw bytes.
func hashFile(rel, path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // The path comes from walking the tree being hashed.
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // Read-only file.

	h := sha256.New()
	h.Write([]byte(rel + "\n"))

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
