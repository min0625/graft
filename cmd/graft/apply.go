// Copyright 2026 The Graft Authors

package main

import (
	"fmt"
	"strings"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply lockfile state to the vendor directory",
		Long: `Reconcile the vendor directory to exactly match graft.lock: add missing
dependencies, remove extras, and realign mismatched content. Versions are
never resolved — apply installs only the locked commits, so it is CI-safe.
graft.toml and graft.lock are never modified.

GRAFT_LINK_MODE selects how dests are materialized: "copy" (default) copies
from the shared content store; "symlink" points each dest at the store with a
directory symlink. It is a per-machine choice (set GRAFT_LINK_MODE=symlink),
requires the vendor directory to be gitignored, and is never recorded in
graft.toml or graft.lock.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := resolveMode()
			if err != nil {
				return err
			}

			p, release, err := openProjectLocked(cmd)
			if err != nil {
				return err
			}
			defer release()

			lf, found, err := p.loadLock()
			if err != nil {
				return err
			}

			if !found {
				return clierr.New(clierr.CodeConfig,
					lockfile.Filename+" not found",
					"run `graft lock` first, then commit the lockfile",
				)
			}

			if err := checkSync(p.manifest, lf); err != nil {
				return err
			}

			result, err := p.reconcile(cmd.Context(), lf, mode)
			if err != nil {
				return err
			}

			printReconcile(cmd.OutOrStdout(), result, "", "")

			if !result.Changed() {
				printf(cmd.OutOrStdout(), "✓ already up to date\n")
			}

			return nil
		},
	}

	return cmd
}

// syncField is one manifest↔lockfile sync-key comparison for a dep.
type syncField struct{ field, manifest, locked string }

// syncFields returns every sync-key comparison for one dep — version, repo,
// subdir, symlinks, and dest (spec §4.4, §4.5). It is the single source of
// truth for the key set, shared by checkSync (apply / lock --check) and the
// status command, so the two can never disagree on what "in sync" means.
func syncFields(m *config.Manifest, dep config.Dep, lf *lockfile.Lockfile, ld *lockfile.LockedDep) []syncField {
	return []syncField{
		{"version", dep.Version, ld.Version},
		{"repo", dep.Repo, ld.Repo},
		{"subdir", dep.Subdir, ld.Subdir},
		{"symlinks", config.NormalizeSymlinks(dep.Symlinks), config.NormalizeSymlinks(ld.Symlinks)},
		{"dest", m.ResolvedDest(dep), lf.Dest(*ld)},
	}
}

// depInSync reports whether every sync key of dep matches its locked entry.
func depInSync(m *config.Manifest, dep config.Dep, lf *lockfile.Lockfile, ld *lockfile.LockedDep) bool {
	for _, f := range syncFields(m, dep, lf, ld) {
		if f.manifest != f.locked {
			return false
		}
	}

	return true
}

// checkSync verifies that graft.toml and graft.lock agree, by pure string
// comparison of every dep's sync keys (see syncFields) — no network.
func checkSync(m *config.Manifest, lf *lockfile.Lockfile) error {
	var diffs []string

	for _, dep := range m.Deps {
		ld := lf.FindDep(dep.Name)
		if ld == nil {
			diffs = append(diffs,
				fmt.Sprintf("dependency %q is in %s but not in %s", dep.Name, config.Filename, lockfile.Filename))

			continue
		}

		for _, f := range syncFields(m, dep, lf, ld) {
			if f.manifest != f.locked {
				diffs = append(diffs, fmt.Sprintf("dependency %q %s differs: %s has %q, %s has %q",
					dep.Name, f.field, config.Filename, f.manifest, lockfile.Filename, f.locked))
			}
		}
	}

	for _, ld := range lf.Deps {
		if m.FindDep(ld.Name) == nil {
			diffs = append(diffs,
				fmt.Sprintf("dependency %q is in %s but not in %s", ld.Name, lockfile.Filename, config.Filename))
		}
	}

	if len(diffs) == 0 {
		return nil
	}

	return clierr.New(clierr.CodeConfig,
		fmt.Sprintf("%s is out of sync with %s", lockfile.Filename, config.Filename),
		strings.Join(diffs, "\n"),
		"run `graft lock` to update the lockfile, then commit it",
	)
}
