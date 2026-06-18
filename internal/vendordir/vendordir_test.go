// Copyright 2026 The Graft Authors

package vendordir_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/hasher"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/min0625/graft/internal/vendordir"
)

const (
	depScripts    = "scripts"
	fileA         = "a.txt"
	lockedContent = "locked\n"
)

// tree is an in-memory dep tree: slash-separated path → content.
type tree map[string]string

func (tr tree) write(t *testing.T, root string) {
	t.Helper()

	for rel, content := range tr {
		full := filepath.Join(root, filepath.FromSlash(rel))

		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (tr tree) hash(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	tr.write(t, dir)

	h, err := hasher.HashTree(dir)
	if err != nil {
		t.Fatal(err)
	}

	return h
}

// fakeFetch serves trees by dep name and records which deps were fetched.
type fakeFetch struct {
	trees   map[string]tree
	fetched []string
	t       *testing.T
}

func (f *fakeFetch) fetch(_ context.Context, dep lockfile.LockedDep, dst string) error {
	f.t.Helper()

	tr, ok := f.trees[dep.Name]
	if !ok {
		f.t.Fatalf("unexpected fetch of %q", dep.Name)
	}

	f.fetched = append(f.fetched, dep.Name)

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}

	tr.write(f.t, dst)

	return nil
}

func lockedDep(t *testing.T, name string, tr tree) lockfile.LockedDep {
	t.Helper()

	return lockfile.LockedDep{
		Name:    name,
		Repo:    "github.com/org/" + name,
		Version: "v1.0.0",
		Commit:  "a3f8c21d4e8f1b2c3d4e5f6a7b8c9d0e1f2a3b4c",
		Hash:    tr.hash(t),
	}
}

// opts builds reconcile Options backed by per-test store and staging
// directories, so the content store never touches the real cache.
func opts(t *testing.T, ff *fakeFetch) vendordir.Options {
	t.Helper()

	return vendordir.Options{
		StoreRoot: filepath.Join(t.TempDir(), "store"),
		TmpDir:    t.TempDir(),
		Fetch:     ff.fetch,
	}
}

// optsWithFetch is like opts but accepts any fetch function, for tests that
// use a custom fetch implementation such as concurrentFetch.
func optsWithFetch(t *testing.T, fetch vendordir.FetchFunc) vendordir.Options {
	t.Helper()

	return vendordir.Options{
		StoreRoot: filepath.Join(t.TempDir(), "store"),
		TmpDir:    t.TempDir(),
		Fetch:     fetch,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // Test-controlled path.
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestReconcile_installsMissingDep(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tr := tree{"run.sh": "#!/bin/sh\n", "lib/util.sh": "util\n"}
	dep := lockedDep(t, depScripts, tr)
	ff := &fakeFetch{t: t, trees: map[string]tree{depScripts: tr}}

	result, err := vendordir.Reconcile(t.Context(), root, "deps", []lockfile.LockedDep{dep}, opts(t, ff))
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Installed) != 1 || result.Installed[0].Name != depScripts {
		t.Errorf("Installed = %+v", result.Installed)
	}

	if got := readFile(t, filepath.Join(root, "deps", depScripts, "lib", "util.sh")); got != "util\n" {
		t.Errorf("installed content = %q", got)
	}

	if _, err := os.Stat(filepath.Join(root, "deps", vendordir.StagingDirName)); !os.IsNotExist(err) {
		t.Error("staging dir survived the reconcile")
	}
}

func TestReconcile_skipsMatchingDep(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tr := tree{fileA: "x\n"}
	dep := lockedDep(t, depScripts, tr)
	tr.write(t, filepath.Join(root, "deps", depScripts))

	ff := &fakeFetch{t: t, trees: map[string]tree{}}

	result, err := vendordir.Reconcile(t.Context(), root, "deps", []lockfile.LockedDep{dep}, opts(t, ff))
	if err != nil {
		t.Fatal(err)
	}

	if result.Changed() {
		t.Errorf("want no-op, got %+v", result)
	}
}

func TestReconcile_replacesModifiedDep(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tr := tree{fileA: lockedContent}
	dep := lockedDep(t, depScripts, tr)

	tree{fileA: "tampered\n", "stray.txt": "x\n"}.write(t, filepath.Join(root, "deps", depScripts))

	ff := &fakeFetch{t: t, trees: map[string]tree{depScripts: tr}}

	result, err := vendordir.Reconcile(t.Context(), root, "deps", []lockfile.LockedDep{dep}, opts(t, ff))
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Installed) != 1 {
		t.Fatalf("Installed = %+v, want the modified dep reinstalled", result.Installed)
	}

	if got := readFile(t, filepath.Join(root, "deps", depScripts, fileA)); got != lockedContent {
		t.Errorf("a.txt = %q, want the locked content restored", got)
	}

	if _, err := os.Stat(filepath.Join(root, "deps", depScripts, "stray.txt")); !os.IsNotExist(err) {
		t.Error("stray file survived the reinstall")
	}
}

func TestReconcile_removesExtras(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tr := tree{fileA: "x\n"}
	dep := lockedDep(t, depScripts, tr)
	tr.write(t, filepath.Join(root, "deps", depScripts))

	tree{"old.txt": "left behind\n"}.write(t, filepath.Join(root, "deps", "removed-dep"))
	tree{"keep.txt": "untouched\n"}.write(t, filepath.Join(root, "outside"))

	ff := &fakeFetch{t: t, trees: map[string]tree{}}

	result, err := vendordir.Reconcile(t.Context(), root, "deps", []lockfile.LockedDep{dep}, opts(t, ff))
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Removed) != 1 || result.Removed[0] != "deps/removed-dep" {
		t.Errorf("Removed = %v, want [deps/removed-dep]", result.Removed)
	}

	if _, err := os.Stat(filepath.Join(root, "deps", "removed-dep")); !os.IsNotExist(err) {
		t.Error("extra dep survived")
	}

	if got := readFile(t, filepath.Join(root, "outside", "keep.txt")); got != "untouched\n" {
		t.Error("reconcile touched a path outside the vendor directory")
	}
}

func TestReconcile_keepsNestedDestsAndPrunesAround(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tr := tree{fileA: "x\n"}
	dep := lockedDep(t, "group/nested", tr)
	tr.write(t, filepath.Join(root, "deps", "group", "nested"))

	tree{"junk.txt": "x\n"}.write(t, filepath.Join(root, "deps", "group", "junk"))

	ff := &fakeFetch{t: t, trees: map[string]tree{}}

	result, err := vendordir.Reconcile(t.Context(), root, "deps", []lockfile.LockedDep{dep}, opts(t, ff))
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Removed) != 1 || result.Removed[0] != "deps/group/junk" {
		t.Errorf("Removed = %v, want [deps/group/junk]", result.Removed)
	}

	if _, err := os.Stat(filepath.Join(root, "deps", "group", "nested", fileA)); err != nil {
		t.Error("nested dest was removed")
	}
}

func TestReconcile_cleansStaleStaging(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staging := filepath.Join(root, "deps", vendordir.StagingDirName)
	tree{"leftover/file.txt": "stale\n"}.write(t, staging)

	tr := tree{fileA: "x\n"}
	dep := lockedDep(t, depScripts, tr)
	ff := &fakeFetch{t: t, trees: map[string]tree{depScripts: tr}}

	result, err := vendordir.Reconcile(t.Context(), root, "deps", []lockfile.LockedDep{dep}, opts(t, ff))
	if err != nil {
		t.Fatal(err)
	}

	// Stale staging is cleaned, not reported as an extra dep.
	if len(result.Removed) != 0 {
		t.Errorf("Removed = %v, want none", result.Removed)
	}

	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Error("staging dir survived the reconcile")
	}
}

func TestReconcile_integrityFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dep := lockedDep(t, depScripts, tree{fileA: lockedContent})

	// The fetch yields different content than the lockfile records.
	ff := &fakeFetch{t: t, trees: map[string]tree{depScripts: {fileA: "evil\n"}}}

	_, err := vendordir.Reconcile(t.Context(), root, "deps", []lockfile.LockedDep{dep}, opts(t, ff))
	if got := clierr.ExitCode(err); got != int(clierr.CodeIntegrity) {
		t.Fatalf("exit code = %d, want %d (error: %v)", got, clierr.CodeIntegrity, err)
	}

	// The failed install must not leave anything at the dest.
	if _, err := os.Stat(filepath.Join(root, "deps", depScripts)); !os.IsNotExist(err) {
		t.Error("dest exists after integrity failure")
	}
}

// concurrentFetch tracks peak concurrent fetch calls and implements a
// release-barrier: every goroutine blocks until `threshold` are simultaneously
// active, then all are released. This gives a deterministic, non-flaky proof
// that the expected number of goroutines ran concurrently.
//
// Set threshold ≤ 1 to disable the barrier (release immediately).
type concurrentFetch struct {
	trees     map[string]tree
	t         *testing.T
	threshold int

	mu      sync.Mutex
	active  int
	maxSeen int
	once    sync.Once
	release chan struct{} // closed when threshold goroutines are simultaneously active
}

func newConcurrentFetch(t *testing.T, trees map[string]tree, threshold int) *concurrentFetch {
	t.Helper()

	cf := &concurrentFetch{
		trees:     trees,
		t:         t,
		threshold: threshold,
		release:   make(chan struct{}),
	}

	if threshold <= 1 {
		cf.once.Do(func() { close(cf.release) })
	}

	return cf
}

func (f *concurrentFetch) fetch(ctx context.Context, dep lockfile.LockedDep, dst string) error {
	f.mu.Lock()
	f.active++

	if f.active > f.maxSeen {
		f.maxSeen = f.active
	}

	if f.active >= f.threshold {
		f.once.Do(func() { close(f.release) })
	}

	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()

	// Block until threshold goroutines are in-flight, or ctx is cancelled.
	// A cancelled context means the threshold was never reached — the test
	// will report the context error so the barrier threshold is the diagnostic.
	select {
	case <-f.release:
	case <-ctx.Done():
		return ctx.Err()
	}

	tr, ok := f.trees[dep.Name]
	if !ok {
		f.t.Errorf("unexpected fetch of %q", dep.Name)

		return nil
	}

	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}

	tr.write(f.t, dst)

	return nil
}

// TestReconcile_defaultFetchConcurrency verifies that the fetch phase spawns
// defaultFetchJobs (16) concurrent workers — more than runtime.NumCPU() on
// most CI machines — so that a slow network does not serialize downloads
// (spec §5.4).
func TestReconcile_defaultFetchConcurrency(t *testing.T) {
	// spec: REQ-JOBS-FETCHDEFAULT
	t.Parallel()

	const numDeps = 20

	const wantConcurrent = 16 // must match vendordir.defaultFetchJobs

	trees := make(map[string]tree, numDeps)
	deps := make([]lockfile.LockedDep, numDeps)
	tr := tree{fileA: lockedContent}

	for i := range numDeps {
		name := fmt.Sprintf("dep%d", i)
		trees[name] = tr
		deps[i] = lockedDep(t, name, tr)
	}

	cf := newConcurrentFetch(t, trees, wantConcurrent)

	root := t.TempDir()
	o := optsWithFetch(t, cf.fetch)

	// The context timeout is the safety valve: if fewer than wantConcurrent
	// workers start (regression to NumCPU), the barrier is never released and
	// the reconcile returns a context error.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	if _, err := vendordir.Reconcile(ctx, root, "deps", deps, o); err != nil {
		t.Fatalf("Reconcile: %v (did the fetch phase start fewer than %d workers?)", err, wantConcurrent)
	}

	cf.mu.Lock()
	maxSeen := cf.maxSeen
	cf.mu.Unlock()

	if maxSeen < wantConcurrent {
		t.Errorf("max concurrent fetches = %d, want >= %d (defaultFetchJobs)", maxSeen, wantConcurrent)
	}
}
