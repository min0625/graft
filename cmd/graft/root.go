// Copyright 2026 The Graft Authors

package main

import "github.com/spf13/cobra"

// version is the binary version shown by `graft --version`. Release builds
// override it via -ldflags "-X main.version=...".
var version = "dev"

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graft",
		Short: "A language-agnostic dependency manager for git repositories",
		Long: `graft is a language-agnostic dependency manager for git repositories —
a replacement for git submodules.

Dependencies are declared in graft.toml, pinned to exact commits and content
hashes in graft.lock, and installed into the vendor directory by graft apply.`,
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		// The explicit RunE keeps the command runnable so help includes the
		// "Usage:" section, which cobra's help template omits for a
		// non-runnable command without subcommands.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	return cmd
}
