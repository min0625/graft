// Copyright 2026 The Graft Authors

package main

import (
	"fmt"
	"time"

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/links"
	"github.com/min0625/graft/internal/repocache"
	"github.com/min0625/graft/internal/store"
	"github.com/spf13/cobra"
)

// staleRepoAge is how long a cached bare repository may go unfetched before
// `graft cache clean` reclaims it. Removing one only costs a re-fetch.
const staleRepoAge = 30 * 24 * time.Hour

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and manage the global cache",
		Long: `Manage graft's user-level global cache (bare repositories and the
content store). The cache is purely a performance layer — deleting it is
always safe — so these commands never touch project files and need no
graft.toml.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newCacheDirCmd(), newCacheVerifyCmd(), newCacheCleanCmd())

	return cmd
}

func newCacheDirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dir",
		Short: "Print the cache directory path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := cachedir.Dir()
			if err != nil {
				return err
			}

			printf(cmd.OutOrStdout(), "%s\n", dir)

			return nil
		},
	}
}

func newCacheVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Re-hash every store entry and delete corrupted ones",
		Long: `Re-hash every content-store entry against its key and delete any that no
longer match. Exits 4 if any corruption was found and removed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			storeRoot, err := cachedir.Store()
			if err != nil {
				return err
			}

			checked, corrupted, err := store.Verify(storeRoot)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			for _, hash := range corrupted {
				printf(out, "✗ removed corrupted entry %s\n", hash)
			}

			if len(corrupted) > 0 {
				return clierr.New(clierr.CodeIntegrity,
					fmt.Sprintf("%d of %d store entries were corrupted and removed", len(corrupted), checked),
					"the affected dependencies will be re-fetched on the next `graft apply`",
				)
			}

			printf(out, "✓ verified %d store %s\n", checked, plural(checked, "entry", "entries"))

			return nil
		},
	}
}

func newCacheCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove unused store entries and stale repositories",
		Long: `Remove content-store entries that no registered link-mode dest references,
along with bare repositories that have not been fetched recently. The cache is
always safe to delete, so to wipe it entirely just remove the directory printed
by 'graft cache dir'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clean(cmd)
		},
	}
}

// clean removes unreferenced store entries and stale bare repositories.
func clean(cmd *cobra.Command) error {
	cacheRoot, err := cachedir.Dir()
	if err != nil {
		return err
	}

	storeRoot, err := cachedir.Store()
	if err != nil {
		return err
	}

	linksDir, err := cachedir.Links()
	if err != nil {
		return err
	}

	referenced, err := links.ReferencedHashes(linksDir)
	if err != nil {
		return err
	}

	removedEntries, err := store.Clean(storeRoot, referenced)
	if err != nil {
		return err
	}

	removedRepos, err := repocache.Clean(cacheRoot, time.Now().Add(-staleRepoAge))
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	if len(removedEntries) == 0 && len(removedRepos) == 0 {
		printf(out, "✓ cache already clean\n")

		return nil
	}

	printf(out, "✓ removed %d store %s and %d cached %s\n",
		len(removedEntries), plural(len(removedEntries), "entry", "entries"),
		len(removedRepos), plural(len(removedRepos), "repository", "repositories"))

	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}
