// Copyright 2026 The Graft Authors

package clierr_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
)

// implExitCodes is the set of exit codes the clierr package defines — the
// implementation side of spec §4.5.
var implExitCodes = map[int]string{
	int(clierr.CodeSuccess):   "success",
	int(clierr.CodeGeneral):   "general error",
	int(clierr.CodeConfig):    "config/lockfile validation error",
	int(clierr.CodeNetwork):   "network error",
	int(clierr.CodeIntegrity): "hash mismatch (content integrity failure)",
}

// designDocs are the version-controlled spec documents whose §4.5 exit-code
// table must stay in sync with the implementation. Listing both the English
// and zh-TW docs also keeps the two translations consistent on exit codes.
var designDocs = []string{
	"docs/design.md",
	"docs/design.zh-TW.md",
}

// TestSpecExitCodes_matchDesignDocs is a spec-conformance test: it parses the
// §4.5 exit-code table out of each design document and asserts the set of
// codes it lists is exactly the set the clierr package defines. A code added
// to the spec but not the code (or vice versa), or the two translations
// drifting apart, fails this test — so the documents are an executable
// contract, not just prose.
func TestSpecExitCodes_matchDesignDocs(t *testing.T) {
	root := repoRoot(t)

	for _, doc := range designDocs {
		t.Run(doc, func(t *testing.T) {
			path := filepath.Join(root, doc)

			data, err := os.ReadFile(path) //nolint:gosec // Test-controlled path under the repo root.
			if err != nil {
				t.Fatalf("read design doc: %v", err)
			}

			got := parseExitCodeTable(t, string(data))

			for code := range implExitCodes {
				if !got[code] {
					t.Errorf("exit code %d is defined in clierr but missing from %s §4.5", code, doc)
				}
			}

			for code := range got {
				if _, ok := implExitCodes[code]; !ok {
					t.Errorf("exit code %d is listed in %s §4.5 but not defined in clierr", code, doc)
				}
			}
		})
	}
}

// exitCodeRowRe matches a markdown table row whose first cell is a bare
// integer, e.g. "| 2 | Config / lockfile validation error |".
var exitCodeRowRe = regexp.MustCompile(`^\|\s*(\d+)\s*\|`)

// parseExitCodeTable returns the set of exit codes listed in the §4.5 table.
// It scans from the "### 4.5" heading to the next heading, collecting every
// row whose first cell is an integer. It fails the test if the section or any
// rows cannot be found, so a renamed section never passes silently.
func parseExitCodeTable(t *testing.T, doc string) map[int]bool {
	t.Helper()

	codes := make(map[int]bool)
	inSection := false

	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(line, "### 4.5") {
			inSection = true

			continue
		}

		if !inSection {
			continue
		}

		// Any subsequent heading ends the §4.5 section.
		if strings.HasPrefix(line, "#") {
			break
		}

		if m := exitCodeRowRe.FindStringSubmatch(line); m != nil {
			code, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("parse exit code %q: %v", m[1], err)
			}

			codes[code] = true
		}
	}

	if !inSection {
		t.Fatal("could not find the \"### 4.5\" exit-code section")
	}

	if len(codes) == 0 {
		t.Fatal("found the §4.5 section but parsed no exit-code rows")
	}

	return codes
}

// repoRoot walks up from the test's working directory to the module root (the
// directory containing go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod walking up from the test directory")
		}

		dir = parent
	}
}
