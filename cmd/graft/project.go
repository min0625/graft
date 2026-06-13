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
	"github.com/min0625/graft/internal/projlock"
	"github.com/min0625/graft/internal/vendordir"
	"github.com/spf13/cobra"
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
	p, _, err := open(context.Background(), nil)

	return p, err
}

// openProjectLocked is openProject for mutating commands (spec §5.7): it
// additionally takes the per-project advisory lock — before reading
// graft.toml — printing a wait hint to the command's stderr when another
// graft process holds it. The caller must defer release.
func openProjectLocked(cmd *cobra.Command) (p *project, release func(), err error) {
	return open(cmd.Context(), cmd.ErrOrStderr())
}

// open finds the project root, takes the advisory lock when lockWarn is
// non-nil, and loads the manifest. release is a no-op for unlocked opens.
func open(ctx context.Context, lockWarn io.Writer) (p *project, release func(), err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("get working directory: %w", err)
	}

	root, err := config.FindRoot(cwd)
	if err != nil {
		return nil, nil, err
	}

	release = func() {}

	if lockWarn != nil {
		release, err = projlock.Acquire(ctx, root, lockWarn)
		if err != nil {
			return nil, nil, err
		}
	}

	m, err := config.Load(filepath.Join(root, config.Filename))
	if err != nil {
		release()

		return nil, nil, err
	}

	return &project{root: root, manifest: m}, release, nil
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

// fetchDep is the vendordir.FetchFunc used by every command: fetch the locked
// commit of dep into dst.
func fetchDep(ctx context.Context, dep lockfile.LockedDep, dst string) error {
	_, err := fetcher.Fetch(ctx, dep.Name, dep.Repo, dep.Commit, dep.Path, dst)

	return err
}

// reconcile brings the vendor directory in line with lf and prints what
// changed, one line per action.
func (p *project) reconcile(ctx context.Context, lf *lockfile.Lockfile, out io.Writer) (*vendordir.Result, error) {
	result, err := vendordir.Reconcile(ctx, p.root, p.manifest.Vendor, lf.Deps, fetchDep)
	if err != nil {
		return nil, err
	}

	for _, dep := range result.Installed {
		printf(out, "✓ installed %s %s (%.7s)\n", dep.Name, dep.Version, dep.Commit)
	}

	for _, path := range result.Removed {
		printf(out, "✓ removed %s\n", path)
	}

	return result, nil
}
