// Copyright 2026 The Graft Authors

package main

import (
	"fmt"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a dependency",
		Long: `Remove a dependency from graft.toml and graft.lock, and delete its
vendor directory. The dependency must exist in graft.toml.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return clierr.New(clierr.CodeConfig,
					"graft remove requires exactly one <name> argument",
					"example: graft remove shared-scripts",
				)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd, args[0])
		},
	}
}

func runRemove(cmd *cobra.Command, name string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	p, release, err := openProjectLocked(cmd)
	if err != nil {
		return err
	}
	defer release()

	found := false

	for _, dep := range p.manifest.Deps {
		if dep.Name == name {
			found = true
			break
		}
	}

	if !found {
		return clierr.New(clierr.CodeConfig,
			fmt.Sprintf("dependency %q not found in graft.toml", name),
			"check the name with `graft status` or inspect graft.toml",
		)
	}

	newDeps := p.manifest.Deps[:0:0]
	for _, dep := range p.manifest.Deps {
		if dep.Name != name {
			newDeps = append(newDeps, dep)
		}
	}

	p.manifest.Deps = newDeps

	prev, found, err := p.loadLock()
	if err != nil {
		return err
	}

	if !found {
		prev = lockfile.New()
	}

	next, err := relock(ctx, p.manifest, prev, nil)
	if err != nil {
		return err
	}

	if err := p.manifest.Write(p.manifestPath()); err != nil {
		return err
	}

	if err := next.Write(p.lockPath()); err != nil {
		return err
	}

	if _, err := p.reconcile(ctx, next, out, linkMode(false), concurrencyJobs(cmd)); err != nil {
		return err
	}

	printf(out, "✓ removed %s\n", name)

	return nil
}
