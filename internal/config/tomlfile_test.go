// Copyright 2026 The Graft Authors

package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/config"
)

const manifestWithComments = `vendor = "vendor"

# Shared tooling scripts — keep in sync with CI pipeline.
[[deps]]
name = "tools"
repo = "github.com/org/tools"
version = "v1.0.0"

# Protocol buffers generated from the service definitions.
[[deps]]
name = "proto"
repo = "github.com/org/proto"
version = "v2.3.1"
subdir = "gen/go"
`

func readManifestFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // path comes from t.TempDir().
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// TestAppendDep_sortsAndKeepsCommentsGlued verifies that AppendDep inserts the
// new block in name order and that each existing comment stays glued to its
// entry even when sorting moves it (spec §3.1).
// spec: REQ-ADD-PRESERVE
func TestAppendDep_sortsAndKeepsCommentsGlued(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	newDep := config.Dep{
		Name:    "scripts",
		Repo:    "github.com/org/scripts",
		Version: "v0.1.0",
	}

	if err := config.AppendDep(path, newDep); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	// Entries are sorted by name: proto, scripts, tools (the original tools/proto
	// order is re-sorted, and the new scripts lands between them).
	protoIdx := strings.Index(got, `name = "proto"`)
	scriptsIdx := strings.Index(got, `name = "scripts"`)
	toolsIdx := strings.Index(got, `name = "tools"`)

	if protoIdx < 0 || scriptsIdx < 0 || toolsIdx < 0 {
		t.Fatalf("not all deps found in manifest:\n%s", got)
	}

	if protoIdx >= scriptsIdx || scriptsIdx >= toolsIdx {
		t.Errorf("dep ordering wrong: proto=%d scripts=%d tools=%d\n%s", protoIdx, scriptsIdx, toolsIdx, got)
	}

	// Each comment must stay directly above its own block, even though tools
	// moved to the end.
	toolsGlued := "# Shared tooling scripts — keep in sync with CI pipeline.\n[[deps]]\nname = \"tools\""
	if !strings.Contains(got, toolsGlued) {
		t.Errorf("tools comment did not travel with its block:\n%s", got)
	}

	protoGlued := "# Protocol buffers generated from the service definitions.\n[[deps]]\nname = \"proto\""
	if !strings.Contains(got, protoGlued) {
		t.Errorf("proto comment did not travel with its block:\n%s", got)
	}

	// No double blank lines introduced by the reordering.
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("double blank line in output:\n%s", got)
	}
}

// TestAppendDep_withSubdir verifies that AppendDep includes the subdir field when set.
// spec: REQ-ADD-PRESERVE
func TestAppendDep_withSubdir(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	if err := config.AppendDep(path, config.Dep{
		Name:    "sdk",
		Repo:    "github.com/org/sdk",
		Version: "v3.0.0",
		Subdir:  "go/client",
	}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	if !strings.Contains(got, `subdir = "go/client"`) {
		t.Errorf("subdir field missing:\n%s", got)
	}
}

// manifestWithInlineComments has inline comments on the name lines and entries
// in non-sorted order, exercising the parser path that an earlier bug got wrong
// (the inline comment defeated name extraction, so sorting misplaced entries).
const manifestWithInlineComments = `dir = "deps"

[[deps]]
name = "beta" # second
repo = "github.com/org/beta"
version = "v1.0.0"

[[deps]]
name = "delta" # fourth
repo = "github.com/org/delta"
version = "v1.3.0"
`

// TestAppendDep_sortsWithInlineCommentNames verifies that a new entry sorts into
// place even when existing name lines carry inline comments, and those comments
// survive (spec §3.1).
// spec: REQ-ADD-PRESERVE
func TestAppendDep_sortsWithInlineCommentNames(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithInlineComments)

	if err := config.AppendDep(path, config.Dep{
		Name:    "alpha",
		Repo:    "github.com/org/alpha",
		Version: "v2.5.0",
	}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	// alpha sorts ahead of beta and delta despite their inline-commented names.
	alphaIdx := strings.Index(got, `name = "alpha"`)
	betaIdx := strings.Index(got, `name = "beta"`)
	deltaIdx := strings.Index(got, `name = "delta"`)

	if alphaIdx < 0 || betaIdx < 0 || deltaIdx < 0 {
		t.Fatalf("not all deps found:\n%s", got)
	}

	if alphaIdx >= betaIdx || betaIdx >= deltaIdx {
		t.Errorf("dep ordering wrong: alpha=%d beta=%d delta=%d\n%s", alphaIdx, betaIdx, deltaIdx, got)
	}

	// The inline comments must still be attached to their name lines.
	if !strings.Contains(got, `name = "beta" # second`) || !strings.Contains(got, `name = "delta" # fourth`) {
		t.Errorf("inline comment on a name line was lost:\n%s", got)
	}
}

// TestUpdateDep_preservesInlineComment verifies that rewriting a value line keeps
// the inline comment that followed it (spec §3.1).
// spec: REQ-ADD-PRESERVE
func TestUpdateDep_preservesInlineComment(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithInlineComments)

	if err := config.UpdateDep(path, config.Dep{
		Name:    "beta",
		Repo:    "github.com/org/beta",
		Version: "v2.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	if !strings.Contains(got, `version = "v2.0.0"`) {
		t.Errorf("version not updated:\n%s", got)
	}

	if !strings.Contains(got, `name = "beta" # second`) {
		t.Errorf("inline comment dropped when rewriting the block:\n%s", got)
	}
}

// TestUpdateDep_preservesCommentsAndSorts verifies that UpdateDep rewrites only
// the target dep's key-value lines, keeps every comment, and re-sorts the
// entries by name (spec §3.1).
// spec: REQ-ADD-PRESERVE
func TestUpdateDep_preservesCommentsAndSorts(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	updated := config.Dep{
		Name:    "tools",
		Repo:    "github.com/org/tools",
		Version: "v1.2.0",
	}

	if err := config.UpdateDep(path, updated); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	// Comment above the block must survive.
	if !strings.Contains(got, "# Shared tooling scripts") {
		t.Errorf("comment above tools block was removed:\n%s", got)
	}

	// Version must be updated.
	if !strings.Contains(got, `version = "v1.2.0"`) {
		t.Errorf("version not updated:\n%s", got)
	}

	// Old version must be gone.
	if strings.Contains(got, `version = "v1.0.0"`) {
		t.Errorf("old version still present:\n%s", got)
	}

	// The proto block and its comment must be unchanged.
	if !strings.Contains(got, "# Protocol buffers generated") {
		t.Errorf("proto comment was removed:\n%s", got)
	}

	if !strings.Contains(got, `version = "v2.3.1"`) {
		t.Errorf("proto version was changed:\n%s", got)
	}

	// Entries are sorted by name: the original tools/proto order becomes
	// proto/tools after the write.
	toolsIdx := strings.Index(got, `name = "tools"`)
	protoIdx := strings.Index(got, `name = "proto"`)

	if toolsIdx < 0 || protoIdx < 0 || protoIdx > toolsIdx {
		t.Errorf("dep ordering wrong: proto=%d tools=%d", protoIdx, toolsIdx)
	}
}

// TestUpdateDep_addsSubdirWhenMissing verifies that UpdateDep inserts a subdir
// field into a block that did not previously have one.
// spec: REQ-ADD-PRESERVE
func TestUpdateDep_addsSubdirWhenMissing(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	if err := config.UpdateDep(path, config.Dep{
		Name:    "tools",
		Repo:    "github.com/org/tools",
		Version: "v1.0.0",
		Subdir:  "cmd",
	}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	if !strings.Contains(got, `subdir = "cmd"`) {
		t.Errorf("subdir field not added:\n%s", got)
	}
}

// TestUpdateDep_removesSubdirWhenCleared verifies that UpdateDep removes a subdir
// field when the updated dep has Subdir == "".
// spec: REQ-ADD-PRESERVE
func TestUpdateDep_removesSubdirWhenCleared(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	// proto has subdir = "gen/go"; clear it.
	if err := config.UpdateDep(path, config.Dep{
		Name:    "proto",
		Repo:    "github.com/org/proto",
		Version: "v2.3.1",
		Subdir:  "",
	}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	if strings.Contains(got, `subdir = "gen/go"`) {
		t.Errorf("old subdir field still present:\n%s", got)
	}
}

// TestRemoveDep_preservesCommentsAndOrdering verifies that RemoveDep removes
// only the target block and leaves all other content verbatim.
// spec: REQ-ADD-PRESERVE
func TestRemoveDep_preservesCommentsAndOrdering(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	if err := config.RemoveDep(path, "tools"); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	// tools block and its preceding comment must be gone.
	if strings.Contains(got, `name = "tools"`) {
		t.Errorf("tools dep still present:\n%s", got)
	}

	if strings.Contains(got, "# Shared tooling scripts") {
		t.Errorf("tools comment not removed:\n%s", got)
	}

	// The comment above proto must survive.
	if !strings.Contains(got, "# Protocol buffers generated") {
		t.Errorf("proto comment was removed:\n%s", got)
	}

	// proto dep must survive unchanged.
	if !strings.Contains(got, `name = "proto"`) {
		t.Errorf("proto dep was removed:\n%s", got)
	}

	if !strings.Contains(got, `version = "v2.3.1"`) {
		t.Errorf("proto version was changed:\n%s", got)
	}
}

// TestRemoveDep_lastDep verifies that removing the only remaining dep leaves
// just the header (vendor line).
// spec: REQ-ADD-PRESERVE
func TestRemoveDep_lastDep(t *testing.T) {
	t.Parallel()

	single := `vendor = "vendor"

[[deps]]
name = "only"
repo = "github.com/org/only"
version = "v1.0.0"
`

	path := writeManifest(t, single)

	if err := config.RemoveDep(path, "only"); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	if strings.Contains(got, "[[deps]]") {
		t.Errorf("[[deps]] block still present:\n%s", got)
	}

	if !strings.Contains(got, `vendor = "vendor"`) {
		t.Errorf("vendor line removed:\n%s", got)
	}
}
