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
path = "gen/go"
`

func readManifestFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // path comes from t.TempDir().
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// TestAppendDep_preservesExistingContent verifies that AppendDep appends a
// new block without modifying existing content (spec §3.1).
// spec: REQ-ADD-PRESERVE
func TestAppendDep_preservesExistingContent(t *testing.T) {
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

	// Original content must be preserved verbatim at the start.
	if !strings.HasPrefix(got, manifestWithComments) {
		t.Errorf("existing content was modified:\ngot:\n%s\nwant prefix:\n%s", got, manifestWithComments)
	}

	// New block must be present.
	if !strings.Contains(got, `name = "scripts"`) {
		t.Errorf("new dep block missing from manifest:\n%s", got)
	}

	// Comments in the original must still be present.
	if !strings.Contains(got, "# Shared tooling scripts") {
		t.Errorf("comment was removed:\n%s", got)
	}

	// Entry ordering: tools must appear before proto, proto before scripts.
	toolsIdx := strings.Index(got, `name = "tools"`)
	protoIdx := strings.Index(got, `name = "proto"`)
	scriptsIdx := strings.Index(got, `name = "scripts"`)

	if toolsIdx < 0 || protoIdx < 0 || scriptsIdx < 0 {
		t.Fatalf("not all deps found in manifest:\n%s", got)
	}

	if toolsIdx >= protoIdx || protoIdx >= scriptsIdx {
		t.Errorf("dep ordering wrong: tools=%d proto=%d scripts=%d", toolsIdx, protoIdx, scriptsIdx)
	}
}

// TestAppendDep_withPath verifies that AppendDep includes the path field when set.
// spec: REQ-ADD-PRESERVE
func TestAppendDep_withPath(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	if err := config.AppendDep(path, config.Dep{
		Name:    "sdk",
		Repo:    "github.com/org/sdk",
		Version: "v3.0.0",
		Path:    "go/client",
	}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	if !strings.Contains(got, `path = "go/client"`) {
		t.Errorf("path field missing:\n%s", got)
	}
}

// TestUpdateDep_preservesCommentsAndOrdering verifies that UpdateDep rewrites
// only the target dep's key-value lines and leaves everything else intact.
// spec: REQ-ADD-PRESERVE
func TestUpdateDep_preservesCommentsAndOrdering(t *testing.T) {
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

	// Entry ordering must be preserved.
	toolsIdx := strings.Index(got, `name = "tools"`)
	protoIdx := strings.Index(got, `name = "proto"`)

	if toolsIdx < 0 || protoIdx < 0 || toolsIdx > protoIdx {
		t.Errorf("dep ordering wrong: tools=%d proto=%d", toolsIdx, protoIdx)
	}
}

// TestUpdateDep_addsPathWhenMissing verifies that UpdateDep inserts a path
// field into a block that did not previously have one.
// spec: REQ-ADD-PRESERVE
func TestUpdateDep_addsPathWhenMissing(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	if err := config.UpdateDep(path, config.Dep{
		Name:    "tools",
		Repo:    "github.com/org/tools",
		Version: "v1.0.0",
		Path:    "cmd",
	}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	if !strings.Contains(got, `path = "cmd"`) {
		t.Errorf("path field not added:\n%s", got)
	}
}

// TestUpdateDep_removesPathWhenCleared verifies that UpdateDep removes a path
// field when the updated dep has Path == "".
// spec: REQ-ADD-PRESERVE
func TestUpdateDep_removesPathWhenCleared(t *testing.T) {
	t.Parallel()

	path := writeManifest(t, manifestWithComments)

	// proto has path = "gen/go"; clear it.
	if err := config.UpdateDep(path, config.Dep{
		Name:    "proto",
		Repo:    "github.com/org/proto",
		Version: "v2.3.1",
		Path:    "",
	}); err != nil {
		t.Fatal(err)
	}

	got := readManifestFile(t, path)

	if strings.Contains(got, `path = "gen/go"`) {
		t.Errorf("old path field still present:\n%s", got)
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
