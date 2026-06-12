// Copyright 2026 The Graft Authors

package main

import (
	"os"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init <vendor>",
		Short: "Create graft.toml with the given vendor directory",
		Long: `Create graft.toml in the current directory, with the given directory as
the root for installed dependencies (e.g. "graft init deps").

The vendor directory has no default: some ecosystems give "vendor/" special
meaning, so the choice is always explicit.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return clierr.New(clierr.CodeConfig,
					"graft init requires the vendor directory argument",
					"usage: graft init <vendor>\nexample: graft init deps",
				)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(config.Filename); err == nil {
				return clierr.New(
					clierr.CodeConfig,
					config.Filename+" already exists",
					"this directory is already a graft project — edit the existing manifest,\nor delete it first to start over",
				)
			}

			m := &config.Manifest{Vendor: args[0]}

			if err := m.Write(config.Filename); err != nil {
				return err
			}

			printf(cmd.OutOrStdout(), "✓ created %s (vendor: %s)\n", config.Filename, args[0])

			return nil
		},
	}
}
