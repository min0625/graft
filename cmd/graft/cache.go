// Copyright 2026 The Graft Authors

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gitrun"
	"github.com/min0625/graft/internal/links"
	"github.com/min0625/graft/internal/repocache"
	"github.com/min0625/graft/internal/store"
	"github.com/spf13/cobra"
)

// staleRepoAge is how long a cached bare repository may go unfetched, and
// staleEntryAge how long a store entry may go unreferenced, before
// `graft cache prune` reclaims it. Removing either only costs a re-fetch; the
// entry floor also keeps prune from racing a concurrent `apply` (see
// store.Clean).
const (
	staleRepoAge  = 30 * 24 * time.Hour
	staleEntryAge = 30 * 24 * time.Hour
)

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

	cmd.AddCommand(newCacheDirCmd(), newCacheVerifyCmd(), newCachePruneCmd(), newCacheCleanCmd())

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

func newCachePruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove unused store entries and stale repositories",
		Long: `Remove content-store entries that no registered link-mode dest references
and have not been used recently, along with bare repositories that have not been
fetched recently. Safe to run periodically (and in CI). To wipe the cache
entirely instead, use 'graft cache clean'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return prune(cmd)
		},
	}
}

// prune removes unreferenced, aged-out store entries and stale bare repos.
func prune(cmd *cobra.Command) error {
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

	now := time.Now()

	removedEntries, entriesFreed, err := store.Clean(storeRoot, referenced, now.Add(-staleEntryAge))
	if err != nil {
		return err
	}

	removedRepos, reposFreed, err := repocache.Clean(cacheRoot, now.Add(-staleRepoAge))
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	if len(removedEntries) == 0 && len(removedRepos) == 0 {
		printf(out, "✓ cache already clean\n")

		return nil
	}

	printf(out, "✓ removed %d store %s (%s) and %d cached %s (%s)\n",
		len(removedEntries), plural(len(removedEntries), "entry", "entries"), humanizeBytes(entriesFreed),
		len(removedRepos), plural(len(removedRepos), "repository", "repositories"), humanizeBytes(reposFreed))

	return nil
}

func newCacheCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove the entire cache",
		Long: `Remove graft's entire global cache — every bare repository and store entry.
The cache is purely a performance layer, so this is always safe; graft re-fetches
on the next 'apply'. To reclaim only unused entries instead, use
'graft cache prune'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return clean(cmd)
		},
	}
}

// clean removes the whole cache directory.
func clean(cmd *cobra.Command) error {
	dir, err := cachedir.Dir()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		printf(cmd.OutOrStdout(), "✓ cache already empty\n")
		return nil
	}

	// Verify ownership before removing: if the directory lacks a CACHEDIR.TAG
	// then GRAFT_CACHE_DIR is likely set to a non-cache path (e.g. / or $HOME).
	if !cachedir.HasTag(dir) {
		return fmt.Errorf(
			"refusing to remove %s: no CACHEDIR.TAG found — GRAFT_CACHE_DIR may point to the wrong directory",
			dir,
		)
	}

	freed, err := cachedir.DirSize(dir)
	if err != nil {
		return err
	}

	if err := gitrun.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove cache: %w", err)
	}

	printf(cmd.OutOrStdout(), "✓ removed the entire cache (%s)\n", humanizeBytes(freed))

	return nil
}

// humanizeBytes formats a byte count with a binary unit for the reclaimed-space
// line, e.g. 1536 → "1.5 KiB".
func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}
