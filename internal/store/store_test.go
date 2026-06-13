// Copyright 2026 The Graft Authors

package store_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/min0625/graft/internal/store"
)

const hashV1 = "sha256:c7d4b6acdb44a552610c48cbffbffcef2a4d1165dcc081927d1c1875fb63a1ee"

// writeTree writes a slash-keyed file map into a fresh directory and returns it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "tree")

	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, []byte(content), 0o644); err != nil { //nolint:gosec // Test fixture.
			t.Fatal(err)
		}
	}

	return dir
}

func TestPath_shardsHexDigest(t *testing.T) {
	t.Parallel()

	got := store.Path("/cache/store", hashV1)
	want := filepath.Join(
		"/cache/store",
		"sha256",
		"c7",
		"d4b6acdb44a552610c48cbffbffcef2a4d1165dcc081927d1c1875fb63a1ee",
	)

	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestInsert_publishesReadOnlyEntry(t *testing.T) {
	t.Parallel()

	storeRoot := filepath.Join(t.TempDir(), "store")
	src := writeTree(t, map[string]string{"run.sh": "#!/bin/sh\n", "lib/util.sh": "x\n"})

	if store.Exists(storeRoot, hashV1) {
		t.Fatal("entry exists before insert")
	}

	path, err := store.Insert(storeRoot, src, hashV1)
	if err != nil {
		t.Fatal(err)
	}

	if path != store.Path(storeRoot, hashV1) {
		t.Errorf("Insert path = %q", path)
	}

	if !store.Exists(storeRoot, hashV1) {
		t.Error("entry missing after insert")
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source tree should have been moved, not copied")
	}

	info, err := os.Stat(filepath.Join(path, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm()&0o222 != 0 {
		t.Errorf("store file is writable: mode %v", info.Mode())
	}
}

func TestInsert_raceUsesExistingEntry(t *testing.T) {
	t.Parallel()

	storeRoot := filepath.Join(t.TempDir(), "store")
	files := map[string]string{"a.txt": "x\n"}

	if _, err := store.Insert(storeRoot, writeTree(t, files), hashV1); err != nil {
		t.Fatal(err)
	}

	// A second insert of the same hash must succeed and discard its source.
	src := writeTree(t, files)

	path, err := store.Insert(storeRoot, src, hashV1)
	if err != nil {
		t.Fatal(err)
	}

	if path != store.Path(storeRoot, hashV1) {
		t.Errorf("Insert path = %q", path)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("losing source tree was not discarded")
	}
}

func TestInsert_concurrentCreators(t *testing.T) {
	t.Parallel()

	storeRoot := filepath.Join(t.TempDir(), "store")
	files := map[string]string{"a.txt": "x\n"}

	const creators = 8

	srcs := make([]string, creators)
	for i := range srcs {
		srcs[i] = writeTree(t, files)
	}

	var (
		wg   sync.WaitGroup
		errs = make([]error, creators)
	)

	for i := range creators {
		wg.Go(func() {
			_, errs[i] = store.Insert(storeRoot, srcs[i], hashV1)
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("creator %d: %v", i, err)
		}
	}

	if !store.Exists(storeRoot, hashV1) {
		t.Error("entry missing after concurrent inserts")
	}
}

func TestMaterialize_copiesWritableTree(t *testing.T) {
	t.Parallel()

	storeRoot := filepath.Join(t.TempDir(), "store")

	path, err := store.Insert(storeRoot, writeTree(t, map[string]string{
		"run.sh":      "#!/bin/sh\n",
		"lib/util.sh": "util\n",
	}), hashV1)
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "dest")
	if err := store.Materialize(path, dst); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "lib", "util.sh")) //nolint:gosec // Test-controlled path.
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "util\n" {
		t.Errorf("materialized content = %q", data)
	}

	info, err := os.Stat(filepath.Join(dst, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm()&0o200 == 0 {
		t.Errorf("materialized file should be writable (copy mode): mode %v", info.Mode())
	}
}
