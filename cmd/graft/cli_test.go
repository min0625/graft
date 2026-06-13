// Copyright 2026 The Graft Authors

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gittest"
)

// Shared fixture literals, named once for the whole package.
const (
	depScripts = "scripts"
	tagV1      = "v1.0.0"
	tagV2      = "v2.0.0"
	contentV1  = "v1\n"
	contentV2  = "v2\n"
	subPath    = "sub"
	contentLib = "lib\n"
	runShPath  = "deps/scripts/run.sh"
)

// runGraft runs the CLI in-process with the given args and returns its
// combined output and error.
func runGraft(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

func mustRunGraft(t *testing.T, args ...string) string {
	t.Helper()

	out, err := runGraft(t, args...)
	if err != nil {
		t.Fatalf("graft %s: %v\n%s", strings.Join(args, " "), err, clierr.Format(err))
	}

	return out
}

func wantExit(t *testing.T, err error, code clierr.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("want error with exit code %d, got nil", code)
	}

	if got := clierr.ExitCode(err); got != int(code) {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, code, err)
	}
}

// newProjectDir creates a fresh directory with a .git marker — so project
// root discovery can never walk above it — and makes it the working
// directory. The global cache (project locks, spec §5.6) is pointed at a
// per-test directory so tests never touch the real user cache.
func newProjectDir(t *testing.T) string {
	t.Helper()

	t.Setenv(cachedir.EnvOverride, filepath.Join(t.TempDir(), "cache"))

	dir := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	return dir
}

// fixtureRemote is a remote with two tagged commits and a "dev" branch tip.
type fixtureRemote struct {
	repo *gittest.Repo
	v1   string // tagged v1.0.0: run.sh = contentV1, sub/lib.sh = "lib\n"
	v2   string // tagged v2.0.0: run.sh = contentV2, sub/lib.sh = "lib\n"
	dev  string // tip of branch dev: dev.txt added
}

func newFixtureRemote(t *testing.T) *fixtureRemote {
	t.Helper()

	r := gittest.New(t)

	r.WriteFile("run.sh", contentV1)
	r.WriteFile("sub/lib.sh", contentLib)
	v1 := r.Commit("v1")
	r.Tag(tagV1)

	r.WriteFile("run.sh", contentV2)
	v2 := r.Commit("v2")
	r.Tag(tagV2)

	r.Branch("dev")
	r.WriteFile("dev.txt", "dev\n")
	dev := r.Commit("dev work")

	return &fixtureRemote{repo: r, v1: v1, v2: v2, dev: dev}
}

func writeProjectFile(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readProjectFile(t *testing.T, dir, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))) //nolint:gosec // Test-controlled path.
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func manifestFor(f *fixtureRemote, version string) string {
	return `vendor = "deps"

[[deps]]
name    = "scripts"
repo    = "` + f.repo.URL() + `"
version = "` + version + `"
`
}
