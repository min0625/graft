// Copyright 2026 The Graft Authors

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/min0625/graft/internal/cachedir"
	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
	"github.com/min0625/graft/internal/hasher"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/min0625/graft/internal/store"
	"github.com/min0625/graft/internal/vendordir"
	"github.com/spf13/cobra"
)

const (
	statusOK        = "ok"
	statusMissing   = "missing"
	statusModified  = "modified"
	statusExtra     = "extra"
	statusOutOfSync = "out of sync"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show sync status of dependencies",
		Long: `Read-only, no network access. Reports the sync state of each dependency
across graft.toml, graft.lock, and the vendor directory.

For link-mode dests, the check is a cheap link-target comparison (the store is
immutable; use 'graft cache verify' to re-hash store entries). A dest
materialized in the other mode — a symlinked dest in copy mode, a real tree in
link mode — reports "modified": that is exactly what 'graft apply' would
rewrite.

Exit codes: 0 when everything is in sync; 1 on vendor-directory drift
(missing/modified/extra); 2 when graft.toml and graft.lock disagree (same
lockfile-out-of-sync code as 'graft lock --check' and 'graft apply'). When both
occur, the higher code (2) wins.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := openProject()
			if err != nil {
				return err
			}

			lf, lockFound, err := p.loadLock()
			if err != nil {
				return err
			}

			if !lockFound {
				lf = lockfile.New()
			}

			rows, err := statusRows(p, lf, lockFound)
			if err != nil {
				return err
			}

			if len(rows) == 0 {
				// No deps to report — say so rather than exiting silently.
				printf(cmd.OutOrStdout(), "✓ no dependencies\n")

				return nil
			}

			exit := clierr.CodeSuccess
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)

			for _, row := range rows {
				mark := "✓"

				if row[2] != statusOK {
					mark = "✗"

					// A toml↔lock disagreement is a lockfile-sync failure
					// (exit 2, like `lock --check`/`apply`); pure vendor drift
					// (missing/modified/extra) is exit 1 (spec §4.5, §4.6).
					code := clierr.CodeGeneral
					if row[2] == statusOutOfSync {
						code = clierr.CodeConfig
					}

					if code > exit {
						exit = code
					}
				}

				//nolint:errcheck // CLI output, like printf.
				fmt.Fprintf(w, "%s %s\t%s\t%s\n", mark, row[0], row[1], row[2])
			}

			w.Flush() //nolint:errcheck,gosec // CLI output, like printf.

			if exit != clierr.CodeSuccess {
				// The status lines above are the output.
				return clierr.New(exit, "")
			}

			return nil
		},
	}

	return cmd
}

// storeRoot resolves the content-store path without creating it, so the
// read-only status command leaves no directories behind.
func storeRoot() (string, error) {
	dir, err := cachedir.Dir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, cachedir.StoreSubdir), nil
}

// statusRows builds one [name, locked info, state] row per manifest dep,
// lock-only dep, and extra vendor path (spec §4.4).
func statusRows(p *project, lf *lockfile.Lockfile, lockFound bool) ([][3]string, error) {
	sr, err := storeRoot()
	if err != nil {
		return nil, err
	}

	mode, err := resolveMode()
	if err != nil {
		return nil, err
	}

	var rows [][3]string

	// Check each manifest dep.
	for _, dep := range p.manifest.Deps {
		status := depStatus(p.root, sr, mode, p.manifest, dep, lf, lockFound)

		locked := "-"

		if status == statusOK || status == statusMissing || status == statusModified {
			// These states imply a lock entry that matches the manifest —
			// show what is pinned. The nil guard is defensive: the states
			// above already guarantee a lock entry exists.
			if ld := lf.FindDep(dep.Name); ld != nil {
				locked = fmt.Sprintf("%.7s (%s)", ld.Commit, ld.Version)
			}
		}

		rows = append(rows, [3]string{dep.Name, locked, status})
	}

	// Deps only in the lockfile are out of sync too (spec §4.5).
	for _, ld := range lf.Deps {
		if p.manifest.FindDep(ld.Name) == nil {
			rows = append(rows, [3]string{ld.Name, "-", statusOutOfSync})
		}
	}

	// Extra paths in the vendor dir. Without a lockfile no dest is owned and
	// everything is already "out of sync" — an extra report would be noise.
	if lockFound {
		extras, err := vendordir.FindExtras(p.root, p.manifest.Dir, lf.Deps)
		if err != nil {
			return nil, err
		}

		for _, extra := range extras {
			rows = append(rows, [3]string{extra, "-", statusExtra})
		}
	}

	return rows, nil
}

// depStatus returns the status string for a single manifest dep.
func depStatus(
	root, sr string,
	mode vendordir.Mode,
	m *config.Manifest,
	dep config.Dep,
	lf *lockfile.Lockfile,
	lockFound bool,
) string {
	if !lockFound {
		return statusOutOfSync
	}

	ld := lf.FindDep(dep.Name)
	if ld == nil {
		return statusOutOfSync
	}

	// Check that all sync keys match (same check as checkSync).
	if dep.Version != ld.Version || dep.Repo != ld.Repo ||
		dep.Subdir != ld.Subdir || m.ResolvedDest(dep) != lf.Dest(*ld) {
		return statusOutOfSync
	}

	destAbs := filepath.Join(root, filepath.FromSlash(lf.Dest(*ld)))

	if _, err := os.Lstat(destAbs); err != nil {
		if os.IsNotExist(err) {
			return statusMissing
		}
	}

	// A link-mode dest is validated by its target, a copy-mode dest by
	// hashing (spec §5.4, §4.5). A dest materialized in the other mode is
	// exactly the drift apply would rewrite (spec §5.4), so it reports
	// modified — status ok must mean apply is a no-op under the same mode.
	_, readlinkErr := os.Readlink(destAbs)
	if isLink := readlinkErr == nil; isLink != (mode == vendordir.ModeLink) {
		return statusModified
	}

	if mode == vendordir.ModeLink {
		return linkStatus(sr, destAbs, *ld)
	}

	got, err := hasher.HashTree(destAbs)
	if err != nil || got != ld.Hash {
		return statusModified
	}

	return statusOK
}

// linkStatus reports the state of a link-mode dest: ok when it points at the
// live store entry for the locked hash, missing when that entry has been
// cleaned away, modified when it points elsewhere.
func linkStatus(sr, destAbs string, ld lockfile.LockedDep) string {
	storePath := store.Path(sr, ld.Hash)

	if !vendordir.LinkMatches(destAbs, storePath) {
		return statusModified
	}

	if !store.Exists(sr, ld.Hash) {
		return statusMissing
	}

	return statusOK
}
