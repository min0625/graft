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
		Use:   "init [dir]",
		Short: "Create graft.toml with the given install directory (default: deps)",
		Long: `Create graft.toml in the current directory.

The optional argument sets the root directory for installed dependencies.
Defaults to "deps" when omitted — use a different name if "deps/" conflicts
with your ecosystem (e.g. Go projects may prefer "third_party").`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(config.Filename); err == nil {
				return clierr.New(
					clierr.CodeConfig,
					config.Filename+" already exists",
					"this directory is already a graft project — edit the existing manifest,\nor delete it first to start over",
				)
			}

			installDir := "deps"
			if len(args) == 1 {
				installDir = args[0]
			}

			m := &config.Manifest{Dir: installDir}

			if err := m.Write(config.Filename); err != nil {
				return err
			}

			printf(cmd.OutOrStdout(), "✓ created %s (dir: %s)\n", config.Filename, installDir)

			return nil
		},
	}
}
