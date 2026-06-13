// Copyright 2026 The Graft Authors

package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/min0625/graft/internal/lockfile"
	"github.com/spf13/cobra"
)

func newLockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lock",
		Short: "Re-sync graft.lock from graft.toml",
		Long: `Re-sync the lockfile from graft.toml. New entries and entries whose repo
or version changed are re-resolved against the remote and fetched to compute
their content hash; unchanged entries keep their locked commit without any
network access. Nothing is installed into the vendor directory.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
}
