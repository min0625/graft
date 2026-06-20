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

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/clierr"
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

// installOptions resolves the global-cache wiring shared by every command
// that materializes the vendor directory: the content store, the
// same-filesystem checkout staging area, the links registry, the fetch
// function that fills the store on a miss, and the materialization mode.
func installOptions(mode vendordir.Mode) (vendordir.Options, error) {
	cacheRoot, err := cachedir.Dir()
	if err != nil {
		return vendordir.Options{}, err
	}

	storeRoot, err := cachedir.Store()
	if err != nil {
		return vendordir.Options{}, err
	}

	tmpDir, err := cachedir.Tmp()
	if err != nil {
		return vendordir.Options{}, err
	}

	linksDir, err := cachedir.Links()
	if err != nil {
		return vendordir.Options{}, err
	}

	fetch := func(ctx context.Context, dep lockfile.LockedDep, dst string) error {
		_, err := fetcher.Fetch(ctx, cacheRoot, dep.Name, dep.Repo, dep.Commit, dep.Version, dep.Subdir, dst)

		return err
	}

	return vendordir.Options{
		StoreRoot: storeRoot,
		TmpDir:    tmpDir,
		LinksDir:  linksDir,
		Fetch:     fetch,
		Mode:      mode,
	}, nil
}

// resolveMode resolves the machine-local materialization mode (spec §5.6,
// §10.11) from GRAFT_LINK_MODE, defaulting to copy. The mode vocabulary aligns
// with uv; graft supports only copy and symlink. It is a per-machine choice
// that every materializing command honors identically, never read from
// graft.toml or graft.lock; for a one-off, set it for a single command
// (GRAFT_LINK_MODE=symlink graft apply).
func resolveMode() (vendordir.Mode, error) {
	switch mode := os.Getenv("GRAFT_LINK_MODE"); mode {
	case "", "copy":
		return vendordir.ModeCopy, nil
	case "symlink":
		return vendordir.ModeLink, nil
	default:
		return 0, clierr.New(clierr.CodeConfig,
			fmt.Sprintf("invalid GRAFT_LINK_MODE %q", mode),
			"supported modes: copy, symlink",
		)
	}
}

// reconcile brings the vendor directory in line with lf and prints what
// changed, one line per action. Installs flow through the global cache: a
// store hit (spec §5.6) needs no network, and a miss fetches into the cache
// staging area before publishing to the store.
func (p *project) reconcile(
	ctx context.Context, lf *lockfile.Lockfile, out io.Writer, mode vendordir.Mode,
) (*vendordir.Result, error) {
	opts, err := installOptions(mode)
	if err != nil {
		return nil, err
	}

	for _, d := range p.manifest.Deps {
		if d.SkipSymlinks() {
			if opts.SkipSymlinks == nil {
				opts.SkipSymlinks = make(map[string]bool)
			}

			opts.SkipSymlinks[d.Name] = true
		}
	}

	result, err := vendordir.Reconcile(ctx, p.root, p.manifest.Dir, lf.Deps, opts)
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
