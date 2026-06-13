// Copyright 2026 The Graft Authors

package vendordir_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

func lockedDep(t *testing.T, name, dest string, tr tree) lockfile.LockedDep {
	t.Helper()

	return lockfile.LockedDep{
		Name:    name,
		Repo:    "github.com/org/" + name,
		Version: "v1.0.0",
		Dest:    dest,
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
	dep := lockedDep(t, depScripts, "deps/scripts", tr)
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
	dep := lockedDep(t, depScripts, "deps/scripts", tr)
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
	dep := lockedDep(t, depScripts, "deps/scripts", tr)

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
	dep := lockedDep(t, depScripts, "deps/scripts", tr)
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
	dep := lockedDep(t, "nested", "deps/group/nested", tr)
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
	dep := lockedDep(t, depScripts, "deps/scripts", tr)
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
	dep := lockedDep(t, depScripts, "deps/scripts", tree{fileA: lockedContent})

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

func TestReconcile_customDestOutsideVendor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tr := tree{"svc.proto": "service\n"}
	dep := lockedDep(t, "proto", "third_party/proto", tr)
	ff := &fakeFetch{t: t, trees: map[string]tree{"proto": tr}}

	result, err := vendordir.Reconcile(t.Context(), root, "deps", []lockfile.LockedDep{dep}, opts(t, ff))
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Installed) != 1 {
		t.Fatalf("Installed = %+v", result.Installed)
	}

	if got := readFile(t, filepath.Join(root, "third_party", "proto", "svc.proto")); got != "service\n" {
		t.Errorf("custom dest content = %q", got)
	}
}
