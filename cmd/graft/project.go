// Copyright 2026 The Graft Authors

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/min0625/graft/internal/config"
	"github.com/min0625/graft/internal/fetcher"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/min0625/graft/internal/vendor"
)

// project is an opened graft project: the discovered root directory and its
// validated manifest.
type project struct {
	root     string
	manifest *config.Manifest
}

// openProject locates the project root from the working directory (spec
// §4.1) and loads the manifest.
func openProject() (*project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}

	root, err := config.FindRoot(cwd)
	if err != nil {
		return nil, err
	}

	m, err := config.Load(filepath.Join(root, config.Filename))
	if err != nil {
		return nil, err
	}

	return &project{root: root, manifest: m}, nil
}

func (p *project) manifestPath() string {
	return filepath.Join(p.root, config.Filename)
}

func (p *project) lockPath() string {
	return filepath.Join(p.root, lockfile.Filename)
}

// loadLock reads the project lockfile. found is false when the file does
// not exist, so each command can give its own hint.
func (p *project) loadLock() (lf *lockfile.Lockfile, found bool, err error) {
	lf, err = lockfile.Load(p.lockPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, err
	}

	return lf, true, nil
}

// printf writes CLI output, ignoring write errors like the fmt.Print family
// does for stdout.
func printf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// fetchDep is the vendor.FetchFunc used by every command: fetch the locked
// commit of dep into dst.
func fetchDep(ctx context.Context, dep lockfile.LockedDep, dst string) error {
	_, err := fetcher.Fetch(ctx, dep.Name, dep.Repo, dep.Commit, dep.Path, dst)

	return err
}

// reconcile brings the vendor directory in line with lf and prints what
// changed, one line per action.
func (p *project) reconcile(ctx context.Context, lf *lockfile.Lockfile, out io.Writer) (*vendor.Result, error) {
	result, err := vendor.Reconcile(ctx, p.root, p.manifest.Vendor, lf.Deps, fetchDep)
	if err != nil {
		return nil, err
	}

	for _, dep := range result.Installed {
		printf(out, "✓ installed %s %s\n", dep.Name, dep.Version)
	}

	for _, path := range result.Removed {
		printf(out, "✓ removed %s\n", path)
	}

	return result, nil
}
