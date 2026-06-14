// Copyright 2026 The Graft Authors

package main

import "github.com/spf13/cobra"

// version and commit are shown by `graft --version`. Release builds stamp
// them via -ldflags "-X main.version=… -X main.commit=…" (.goreleaser.yaml).
var (
	version = "dev"
	commit  = ""
)

// buildVersion renders the --version string: the version, plus the commit
// when a release build stamped one.
func buildVersion() string {
	if commit == "" {
		return version
	}

	return version + " (" + commit + ")"
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graft",
		Short: "A language-agnostic dependency manager for git repositories",
		Long: `graft is a language-agnostic dependency manager for git repositories —
a replacement for git submodules.

Dependencies are declared in graft.toml, pinned to exact commits and content
hashes in graft.lock, and installed into the vendor directory by graft apply.`,
		Version:       buildVersion(),
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

	cmd.PersistentFlags().Int("jobs", 0,
		"maximum number of concurrent fetch workers (0 = default: 16 for fetch, NumCPU for install; also GRAFT_CONCURRENCY)")

	cmd.AddCommand(
		newInitCmd(),
		newAddCmd(),
		newRemoveCmd(),
		newApplyCmd(),
		newLockCmd(),
		newStatusCmd(),
		newCacheCmd(),
	)

	return cmd
}
