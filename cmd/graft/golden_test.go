// Copyright 2026 The Graft Authors

package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gittest"
)

// Golden file tests (spec §8): every command's stdout/stderr — success and
// failure — is captured as a transcript, normalized, and compared against
// testdata/golden/<name>.golden. Regenerate with:
//
//	go test ./cmd/graft -run TestGolden -update
var update = flag.Bool("update", false, "rewrite golden files")

// goldenDir is resolved at process start, while the working directory is
// still the package directory — tests chdir into temp project dirs.
var goldenDir = func() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	return filepath.Join(wd, "testdata", "golden")
}()

// goldenStep is one step of a golden scenario: a graft invocation, or a
// filesystem mutation between invocations when fn is set. The setup closures
// capture the test's t.
type goldenStep struct {
	args []string
	fn   func()
}

func graft(args ...string) goldenStep { return goldenStep{args: args} }

func setup(fn func()) goldenStep { return goldenStep{fn: fn} }

// runGraftStreams runs the CLI in-process and returns stdout and stderr —
// with a returned error rendered onto stderr exactly like main() does — plus
// the process exit code.
func runGraftStreams(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	var out, errOut bytes.Buffer

	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())
	if err != nil {
		errOut.WriteString(clierr.Format(err))
	}

	return out.String(), errOut.String(), clierr.ExitCode(err)
}

var (
	pseudoTimestampRe = regexp.MustCompile(`v0\.0\.0-\d{14}-`)
	contentHashRe     = regexp.MustCompile(`sha256:[0-9a-f]{64}`)
	// The reason paragraph carries raw git stderr, which varies across git
	// versions — normalize the whole paragraph (up to the next blank line).
	gitReasonRe = regexp.MustCompile(`(?s)(reason: ).*?\n\n`)
)

// remoteRepls maps every spelling of a fixture remote — its URL and the
// full, 12-, and 7-character forms of its commits — to stable placeholders.
func remoteRepls(f *fixtureRemote, label string) []string {
	repls := make([]string, 0, 20)
	repls = append(repls, f.repo.URL(), "<"+label+"-url>")

	for name, sha := range map[string]string{"v1": f.v1, "v2": f.v2, "dev": f.dev} {
		placeholder := "<commit-" + label + "-" + name + ">"
		repls = append(repls, sha, placeholder, sha[:12], placeholder, sha[:7], placeholder)
	}

	return repls
}

// runGolden executes the steps, builds a normalized transcript, and compares
// it to (or, with -update, rewrites) testdata/golden/<name>.golden.
func runGolden(t *testing.T, name string, repls []string, steps []goldenStep) {
	t.Helper()

	var b strings.Builder

	for _, step := range steps {
		if step.fn != nil {
			step.fn()

			continue
		}

		stdout, stderr, exitCode := runGraftStreams(t, step.args...)

		fmt.Fprintf(&b, "$ graft %s\n", strings.Join(step.args, " "))
		b.WriteString(stdout)
		b.WriteString(stderr)

		if exitCode != 0 {
			fmt.Fprintf(&b, "[exit %d]\n", exitCode)
		}

		b.WriteString("\n")
	}

	got := strings.NewReplacer(repls...).Replace(b.String())
	got = pseudoTimestampRe.ReplaceAllString(got, "v0.0.0-<timestamp>-")
	got = contentHashRe.ReplaceAllString(got, "sha256:<hash>")
	got = gitReasonRe.ReplaceAllString(got, "${1}<git stderr>\n\n")

	path := filepath.Join(goldenDir, name+".golden")

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(path) //nolint:gosec // Test-controlled path.
	if err != nil {
		t.Fatalf("read golden file (regenerate with -update): %v", err)
	}

	if got != string(want) {
		t.Errorf("transcript differs from %s (regenerate with -update):\n--- want ---\n%s\n--- got ---\n%s",
			path, want, got)
	}
}

// spec: REQ-INIT-ARG (graft init with no arg), REQ-INIT-NOCLOBBER (second
// init deps fails because graft.toml already exists).
func TestGolden_init(t *testing.T) {
	newProjectDir(t)

	runGolden(t, "init", nil, []goldenStep{
		graft("init"),
		graft("init", "deps"),
		graft("init", "deps"),
	})
}

// spec: REQ-ROOT-NOTFOUND
func TestGolden_noProject(t *testing.T) {
	newProjectDir(t)

	runGolden(t, "no_project", nil, []goldenStep{
		graft("lock"),
		graft("apply"),
		graft("status"),
		graft("add", "github.com/org/repo@v1.0.0"),
		graft("remove", "repo"),
	})
}

// spec: REQ-ADD-TAG (tag recorded as version + commit locked),
// REQ-ADD-NOOP (re-adding the same commit is a no-op).
func TestGolden_addTag(t *testing.T) {
	f := newFixtureRemote(t)
	newProjectDir(t)

	runGolden(t, "add_tag", remoteRepls(f, "remote"), []goldenStep{
		graft("init", "deps"),
		graft("add", f.repo.URL()+"@"+tagV1),
		graft("add", f.repo.URL()+"@"+tagV2),
		graft("add", f.repo.URL()+"@"+tagV2),
	})
}

// spec: REQ-ADD-PSEUDO (branch ref → pseudo-version),
// REQ-ADD-LATEST (no-tag remote falls back to HEAD pseudo-version).
func TestGolden_addBranchAndLatest(t *testing.T) {
	f := newFixtureRemote(t)

	// A second remote without any tag: @latest falls back to the remote
	// HEAD with a pseudo-version.
	untagged := gittest.New(t)
	untagged.WriteFile("tool.sh", "tool\n")
	untaggedSHA := untagged.Commit("init")

	newProjectDir(t)

	repls := append(remoteRepls(f, "remote"),
		untagged.URL(), "<untagged-url>",
		untaggedSHA, "<commit-untagged>",
		untaggedSHA[:12], "<commit-untagged>",
		untaggedSHA[:7], "<commit-untagged>",
	)

	runGolden(t, "add_branch_and_latest", repls, []goldenStep{
		graft("init", "deps"),
		graft("add", f.repo.URL()+"@dev"),
		graft("add", untagged.URL(), "--name", "tools"),
	})
}

// spec: REQ-ADD-RESOLVE (entry-resolution validation errors, exit code 2,
// before any network access).
func TestGolden_addErrors(t *testing.T) {
	f := newFixtureRemote(t)
	other := newFixtureRemote(t) // also derives the name "remote"
	newProjectDir(t)

	repls := append(remoteRepls(f, "remote"), remoteRepls(other, "other")...)

	runGolden(t, "add_errors", repls, []goldenStep{
		graft("init", "deps"),
		graft("add"),
		graft("add", f.repo.URL()+"@"+tagV1),
		graft("add", other.repo.URL()+"@"+tagV1),
		graft("add", f.repo.URL()+"@"+tagV2, "--name", "remote2"),
		graft("add", f.repo.URL()+"@"+tagV1),
		graft("add", f.repo.URL()+"@"+tagV1, "--name", "bad name"),
	})
}

// spec: REQ-EXIT-NET (an unreachable remote fails with exit code 3).
func TestGolden_addUnreachable(t *testing.T) {
	newProjectDir(t)

	runGolden(t, "add_unreachable", nil, []goldenStep{
		graft("init", "deps"),
		graft("add", "file:///nonexistent/graft-no-such-repo@v1.0.0"),
	})
}

// spec: REQ-LOCK-RESYNC (graft lock regenerates graft.lock without installing).
func TestGolden_lock(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)

	runGolden(t, "lock", remoteRepls(f, "remote"), []goldenStep{
		graft("init", "deps"),
		setup(func() { writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1)) }),
		graft("lock"),
		graft("lock"),
	})
}

// spec: REQ-APPLY-NOLOCK, REQ-APPLY-RECONCILE, REQ-APPLY-NOOP,
// REQ-APPLY-REPAIR, REQ-APPLY-SYNC — the inline step comments map each
// invocation to its requirement.
func TestGolden_apply(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)

	runGolden(t, "apply", remoteRepls(f, "remote"), []goldenStep{
		graft("init", "deps"),
		setup(func() { writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1)) }),
		graft("apply"), // graft.lock missing      -> REQ-APPLY-NOLOCK
		graft("lock"),
		graft("apply"), // fresh install            -> REQ-APPLY-RECONCILE
		graft("apply"), // no-op                     -> REQ-APPLY-NOOP
		setup(func() { writeProjectFile(t, dir, runShPath, "tampered\n") }),
		graft("apply"), // repairs hand-edited tree  -> REQ-APPLY-REPAIR
		setup(func() { writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV2)) }),
		graft("apply"), // out of sync               -> REQ-APPLY-SYNC
	})
}

// doctorLockfileHashes invalidates the hash of every locked dep.
func doctorLockfileHashes(t *testing.T, dir string) {
	t.Helper()

	lock := readProjectFile(t, dir, "graft.lock")
	doctored := regexp.MustCompile(`hash = "sha256:[0-9a-f]+"`).
		ReplaceAllString(lock, `hash = "sha256:`+strings.Repeat("0", 64)+`"`)
	writeProjectFile(t, dir, "graft.lock", doctored)
}

// spec: REQ-INTEGRITY (a content-hash mismatch fails with exit code 4).
func TestGolden_applyIntegrity(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)

	runGolden(t, "apply_integrity", remoteRepls(f, "remote"), []goldenStep{
		graft("init", "deps"),
		setup(func() { writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1)) }),
		graft("lock"),
		setup(func() { doctorLockfileHashes(t, dir) }),
		graft("apply"),
	})
}

// TestGolden_applyIntegrityMulti: the spec §5.4 parallel reconcile collects
// errors — both integrity failures land in one transcript.
//
// spec: REQ-PARALLEL-COLLECT
func TestGolden_applyIntegrityMulti(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)

	runGolden(t, "apply_integrity_multi", remoteRepls(f, "remote"), []goldenStep{
		graft("init", "deps"),
		setup(func() { writeProjectFile(t, dir, "graft.toml", multiDepManifest(f, 2)) }),
		graft("lock"),
		setup(func() { doctorLockfileHashes(t, dir) }),
		graft("apply"),
	})
}

// spec: REQ-REMOVE-MISSING (remove of an unknown name fails with exit code 2).
func TestGolden_remove(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)

	runGolden(t, "remove", remoteRepls(f, "remote"), []goldenStep{
		graft("init", "deps"),
		setup(func() { writeProjectFile(t, dir, "graft.toml", manifestFor(f, tagV1)) }),
		graft("lock"),
		graft("apply"),
		graft("remove", "scripts"),
		graft("remove", "nosuch"),
	})
}

// spec: REQ-STATUS-STATES (ok/missing/modified/extra/out-of-sync),
// REQ-STATUS-EXIT (exit 0 when all ok, exit 1 on drift).
func TestGolden_status(t *testing.T) {
	f := newFixtureRemote(t)
	dir := newProjectDir(t)

	runGolden(t, "status", remoteRepls(f, "remote"), []goldenStep{
		graft("init", "deps"),
		setup(func() { writeProjectFile(t, dir, "graft.toml", multiDepManifest(f, 2)) }),
		graft("status"), // no lockfile: everything out of sync
		graft("lock"),
		graft("apply"),
		graft("status"), // all ok
		setup(func() {
			// dep0 modified, dep1 missing, plus an extra path.
			writeProjectFile(t, dir, "deps/dep0/run.sh", "tampered\n")

			if err := os.RemoveAll(filepath.Join(dir, "deps", "dep1")); err != nil {
				t.Fatal(err)
			}

			if err := os.MkdirAll(filepath.Join(dir, "deps", "junk"), 0o750); err != nil {
				t.Fatal(err)
			}

			writeProjectFile(t, dir, "deps/junk/file.txt", "junk\n")
		}),
		graft("status"),
		setup(func() { writeProjectFile(t, dir, "graft.toml", multiDepManifest(f, 1)) }),
		graft("status"), // graft.toml edited without re-locking: out of sync
	})
}
