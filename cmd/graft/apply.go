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
	var linkMode string

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply lockfile state to the vendor directory",
		Long: `Reconcile the vendor directory to exactly match graft.lock: add missing
dependencies, remove extras, and realign mismatched content. Versions are
never resolved — apply installs only the locked commits, so it is CI-safe.
graft.toml and graft.lock are never modified.

--link-mode (or GRAFT_LINK_MODE) selects how dests are materialized:
"copy" (default) copies from the shared content store; "symlink" points each
dest at the store with a directory symlink. symlink is a machine-local choice
and requires the vendor directory to be gitignored.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := resolveMode(linkMode)
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

			result, err := p.reconcile(cmd.Context(), lf, cmd.OutOrStdout(), mode)
			if err != nil {
				return err
			}

			if !result.Changed() {
				printf(cmd.OutOrStdout(), "✓ already up to date\n")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&linkMode, "link-mode", "",
		"how to materialize dests from the content store: copy (default) or symlink")

	return cmd
}

// checkSync verifies that graft.toml and graft.lock agree, by pure string
// comparison of every dep's version, repo, path, and resolved dest
// (spec §4.3) — no network.
func checkSync(m *config.Manifest, lf *lockfile.Lockfile) error {
	var diffs []string

	for _, dep := range m.Deps {
		ld := lf.FindDep(dep.Name)
		if ld == nil {
			diffs = append(diffs,
				fmt.Sprintf("dependency %q is in %s but not in %s", dep.Name, config.Filename, lockfile.Filename))

			continue
		}

		for _, f := range []struct{ field, manifest, locked string }{
			{"version", dep.Version, ld.Version},
			{"repo", dep.Repo, ld.Repo},
			{"path", dep.Path, ld.Path},
			{"dest", m.ResolvedDest(dep), lf.Dest(*ld)},
		} {
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
