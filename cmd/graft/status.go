// Copyright 2026 The Graft Authors

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
	"github.com/min0625/graft/internal/hasher"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/min0625/graft/internal/vendor"
	"github.com/spf13/cobra"
)

const statusOutOfSync = "out of sync"

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync status of dependencies",
		Long: `Read-only, no network access. Reports the sync state of each dependency
across graft.toml, graft.lock, and the vendor directory.

Exit code 0 when all dependencies are ok; exit code 1 if any drift is detected.`,
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

			dirty := false
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)

			for _, row := range rows {
				mark := "✓"
				if row[2] != "ok" {
					mark = "✗"
					dirty = true
				}

				//nolint:errcheck // CLI output, like printf.
				fmt.Fprintf(w, "%s %s\t%s\t%s\n", mark, row[0], row[1], row[2])
			}

			w.Flush() //nolint:errcheck,gosec // CLI output, like printf.

			if dirty {
				// Exit 1 signals drift; the status lines above are the output.
				return clierr.New(clierr.CodeGeneral, "")
			}

			return nil
		},
	}
}

// statusRows builds one [name, locked info, state] row per manifest dep,
// lock-only dep, and extra vendor path (spec §4.4).
func statusRows(p *project, lf *lockfile.Lockfile, lockFound bool) ([][3]string, error) {
	var rows [][3]string

	// Check each manifest dep.
	for _, dep := range p.manifest.Deps {
		status := depStatus(p.root, p.manifest, dep, lf, lockFound)

		locked := "-"

		if status == "ok" || status == "missing" || status == "modified" {
			// These states imply a lock entry that matches the manifest —
			// show what is pinned.
			ld := lf.FindDep(dep.Name)
			locked = fmt.Sprintf("%.7s (%s)", ld.Commit, ld.Version)
		}

		rows = append(rows, [3]string{dep.Name, locked, status})
	}

	// Deps only in the lockfile are out of sync too (spec §4.4).
	for _, ld := range lf.Deps {
		if p.manifest.FindDep(ld.Name) == nil {
			rows = append(rows, [3]string{ld.Name, "-", statusOutOfSync})
		}
	}

	// Extra paths in the vendor dir. Without a lockfile no dest is owned and
	// everything is already "out of sync" — an extra report would be noise.
	if lockFound {
		extras, err := findExtras(p.root, p.manifest.Vendor, lf.Deps)
		if err != nil {
			return nil, err
		}

		for _, extra := range extras {
			rows = append(rows, [3]string{extra, "-", "extra"})
		}
	}

	return rows, nil
}

// depStatus returns the status string for a single manifest dep.
func depStatus(
	root string,
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
		dep.Path != ld.Path || m.ResolvedDest(dep) != ld.Dest {
		return statusOutOfSync
	}

	destAbs := filepath.Join(root, filepath.FromSlash(ld.Dest))

	if _, err := os.Lstat(destAbs); err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
	}

	got, err := hasher.HashTree(destAbs)
	if err != nil || got != ld.Hash {
		return "modified"
	}

	return "ok"
}

// findExtras returns paths under vendorDir that are not owned by any locked
// dep. The returned paths are relative to the project root, slash-separated.
func findExtras(root, vendorDir string, deps []lockfile.LockedDep) ([]string, error) {
	vendorAbs := filepath.Join(root, filepath.FromSlash(vendorDir))

	if _, err := os.Stat(vendorAbs); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	owned := make(map[string]bool, len(deps))
	for _, dep := range deps {
		owned[dep.Dest] = true
	}

	var extras []string

	var walk func(abs, rel string) error

	walk = func(abs, rel string) error {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return err
		}

		for _, e := range entries {
			eAbs := filepath.Join(abs, e.Name())
			eRel := rel + "/" + e.Name()

			switch {
			case rel == vendorDir && e.Name() == vendor.StagingDirName:
				// Never an extra (spec §5.3).
			case owned[eRel]:
				// A locked dest owns this entry.
			case e.IsDir() && ownsBelow(eRel, owned):
				if err := walk(eAbs, eRel); err != nil {
					return err
				}
			default:
				extras = append(extras, eRel)
			}
		}

		return nil
	}

	return extras, walk(vendorAbs, vendorDir)
}

// ownsBelow reports whether any owned dest lies strictly below dir.
func ownsBelow(dir string, owned map[string]bool) bool {
	prefix := dir + "/"
	for dest := range owned {
		if strings.HasPrefix(dest, prefix) {
			return true
		}
	}

	return false
}
