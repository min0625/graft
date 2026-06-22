// Copyright 2026 The Graft Authors

package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/spf13/cobra"
)

func newLockCmd() *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Re-sync graft.lock from graft.toml",
		Long: `Re-sync the lockfile from graft.toml. New entries and entries whose repo
or version changed are re-resolved against the remote and fetched to compute
their content hash; unchanged entries keep their locked commit without any
network access. Nothing is installed into the vendor directory.

Use --check to verify the lockfile is up to date without writing any files
(exits 0 if in sync, 2 if not). Intended for CI gates.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if check {
				return runLockCheck(cmd)
			}

			p, release, err := openProjectLocked(cmd)
			if err != nil {
				return err
			}
			defer release()

			prev, found, err := p.loadLock()
			if err != nil {
				return err
			}

			if !found {
				prev = lockfile.New()
			}

			next, err := relock(cmd.Context(), p.manifest, prev, nil)
			if err != nil {
				return err
			}

			data, err := next.Encode()
			if err != nil {
				return err
			}

			if old, err := os.ReadFile(p.lockPath()); err == nil && bytes.Equal(old, data) {
				printf(cmd.OutOrStdout(), "✓ %s is up to date\n", lockfile.Filename)

				return nil
			}

			//nolint:gosec // The lockfile is committed, world-readable.
			if err := os.WriteFile(p.lockPath(), data, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", lockfile.Filename, err)
			}

			printf(cmd.OutOrStdout(), "✓ updated %s\n", lockfile.Filename)

			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false,
		"verify graft.lock is up to date without writing any files (exit 0 if in sync, 2 if not)")

	return cmd
}

func runLockCheck(cmd *cobra.Command) error {
	p, err := openProject()
	if err != nil {
		return err
	}

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

	// Share apply's diff so `lock --check` and `apply` report drift
	// identically (same fields, same wording, same indentation).
	if err := checkSync(p.manifest, lf); err != nil {
		return err
	}

	printf(cmd.OutOrStdout(), "✓ %s is up to date\n", lockfile.Filename)

	return nil
}
