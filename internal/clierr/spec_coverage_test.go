// Copyright 2026 The Graft Authors

package clierr_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// requirementsDoc is the traceability matrix parsed by the coverage test.
const requirementsDoc = "docs/requirements.md"

// reqIDRe matches a requirement ID, e.g. "REQ-APPLY-NOLOCK". The token part
// allows uppercase letters and digits.
var reqIDRe = regexp.MustCompile(`REQ-[A-Z]+-[A-Z0-9]+`)

// TestSpecCoverage_requirementsAreTested is a spec-conformance test that makes
// docs/requirements.md an executable contract: every requirement listed there
// must be referenced by at least one test (via a "// spec: REQ-…" comment),
// and every REQ-… referenced by a test must exist in the table. The first
// rule forces a covering test for each documented requirement; the second
// catches typos and stale IDs. A human verifies behavior against the spec by
// reading a requirement, following its ID to the annotated test, and reading
// what that test asserts.
func TestSpecCoverage_requirementsAreTested(t *testing.T) {
	root := repoRoot(t)

	declared := parseRequirementIDs(t, filepath.Join(root, requirementsDoc))
	if len(declared) == 0 {
		t.Fatalf("no requirement IDs parsed from %s", requirementsDoc)
	}

	referenced := scanTestReferences(t, root)

	for id := range declared {
		if len(referenced[id]) == 0 {
			t.Errorf("requirement %s is declared in %s but referenced by no test "+
				"(add a \"// spec: %s\" comment to the covering test)", id, requirementsDoc, id)
		}
	}

	for id, files := range referenced {
		if !declared[id] {
			t.Errorf("test(s) reference %s, which is not declared in %s: %s",
				id, requirementsDoc, strings.Join(files, ", "))
		}
	}
}

// parseRequirementIDs returns the set of requirement IDs declared in the
// markdown table, i.e. table rows whose first cell is a REQ-… ID.
func parseRequirementIDs(t *testing.T, path string) map[string]bool {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // Test-controlled path under the repo root.
	if err != nil {
		t.Fatalf("read requirements doc: %v", err)
	}

	ids := make(map[string]bool)

	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "| REQ-") {
			continue
		}

		if id := reqIDRe.FindString(line); id != "" {
			if ids[id] {
				t.Errorf("duplicate requirement ID %s in %s", id, requirementsDoc)
			}

			ids[id] = true
		}
	}

	return ids
}

// scanTestReferences walks the repo and collects, for every REQ-… token found
// in a *_test.go file, the set of files referencing it. By convention the
// token lives in a "// spec: REQ-…" comment, but any occurrence counts, so
// wrapped comments and inline step annotations are picked up too. This test
// file is skipped so its own regex literal is not counted.
func scanTestReferences(t *testing.T, root string) map[string][]string {
	t.Helper()

	refs := make(map[string][]string)
	self := "spec_coverage_test.go"

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(d.Name(), "_test.go") || d.Name() == self {
			return nil
		}

		data, err := os.ReadFile(path) //nolint:gosec // Walking test files under the repo root.
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}

		for _, id := range reqIDRe.FindAllString(string(data), -1) {
			refs[id] = append(refs[id], rel)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk repo for test references: %v", err)
	}

	return refs
}
