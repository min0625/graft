// Copyright 2026 The Graft Authors

package main

import (
	"fmt"
	"strings"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/min0625/graft/internal/resolver"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		nameFlag string
		destFlag string
	)

	cmd := &cobra.Command{
		Use:   "add <repo>@<ref> | <name>@<ref>",
		Short: "Add or update a dependency",
		Long: `Add a dependency, or change the version of an existing one — there is no
separate update command. The ref may be a tag, a branch, or a full or
partial commit SHA; tags are recorded as the version, anything untagged
becomes a pseudo-version, and the resolved commit is pinned in graft.lock.

For a dependency that already exists in graft.toml the first argument may be
its name instead of the repo, e.g. "graft add shared-scripts@v1.3.0".

After updating its entry, add re-syncs the whole lockfile (like graft lock)
and reconciles the vendor directory (like graft apply).`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return clierr.New(clierr.CodeConfig,
					"graft add requires exactly one <repo>@<ref> argument",
					"example: graft add github.com/org/shared-scripts@v1.2.0",
				)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(cmd, args[0], nameFlag, destFlag)
		},
	}

	cmd.Flags().
		StringVar(&nameFlag, "name", "", "local identifier for the dependency (default: last repo path segment)")
	cmd.Flags().StringVar(&destFlag, "dest", "", "local destination directory (default: <vendor>/<name>)")

	return cmd
}

func runAdd(cmd *cobra.Command, spec, nameFlag, destFlag string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	base, ref := splitSpec(spec)
	if ref == "" {
		return clierr.New(clierr.CodeGeneral,
			"a ref is required: graft add <repo>@<ref>",
			"pass a tag, branch, or commit SHA — @latest resolution is not implemented yet",
		)
	}

	p, err := openProject()
	if err != nil {
		return err
	}

	dep, repo, err := targetDep(p.manifest, base, nameFlag)
	if err != nil {
		return err
	}

	res, err := resolver.ResolveRef(ctx, repo, ref)
	if err != nil {
		return err
	}

	version := resolver.PseudoVersion(res.Time, res.Commit)
	if res.IsTag {
		version = ref
	}

	prev, found, err := p.loadLock()
	if err != nil {
		return err
	}

	if !found {
		prev = lockfile.New()
	}

	// The same locked commit is a no-op for the entry: keep the stored
	// version string (spec §4.2).
	isNew := dep == nil
	already := false

	if dep != nil {
		if ld := prev.FindDep(dep.Name); ld != nil && ld.Repo == repo && ld.Commit == res.Commit {
			already = true
			version = ld.Version
		}
	}

	if dep == nil {
		p.manifest.Deps = append(p.manifest.Deps, config.Dep{Name: config.DefaultName(repo)})
		dep = &p.manifest.Deps[len(p.manifest.Deps)-1]
	}

	dep.Repo = repo
	dep.Version = version

	if nameFlag != "" {
		dep.Name = nameFlag
	}

	if cmd.Flags().Changed("dest") {
		dep.Dest = destFlag
	}

	if err := p.manifest.Write(p.manifestPath()); err != nil {
		return err
	}

	next, err := relock(ctx, p.manifest, prev, map[string]resolver.Resolution{dep.Name: res})
	if err != nil {
		return err
	}

	if err := next.Write(p.lockPath()); err != nil {
		return err
	}

	if _, err := p.reconcile(ctx, next, out); err != nil {
		return err
	}

	switch {
	case already:
		printf(out, "✓ %s already at %.7s (%s)\n", dep.Name, res.Commit, version)
	case isNew:
		printf(out, "✓ added %s %s (%.7s)\n", dep.Name, version, res.Commit)
	default:
		printf(out, "✓ updated %s to %s (%.7s)\n", dep.Name, version, res.Commit)
	}

	return nil
}

// targetDep decides which manifest entry the add targets: a base containing
// "/" is a repo path (normalized; the dep may be new), anything else must be
// the name of an existing dep (spec §4.2 — names can never contain "/").
func targetDep(m *config.Manifest, base, nameFlag string) (dep *config.Dep, repo string, err error) {
	if strings.Contains(base, "/") {
		repo = normalizeRepo(base)

		name := nameFlag
		if name == "" {
			name = config.DefaultName(repo)
		}

		return m.FindDep(name), repo, nil
	}

	dep = m.FindDep(base)
	if dep == nil {
		return nil, "", clierr.New(clierr.CodeConfig,
			fmt.Sprintf("unknown dependency %q", base),
			"no dependency with that name exists in "+config.Filename,
			"pass a repository path (e.g. github.com/org/repo@v1.0.0) to add a new dependency",
		)
	}

	return dep, dep.Repo, nil
}

// splitSpec splits <base>@<ref>. The candidate ref must not contain ":" —
// git forbids it in refnames — which keeps the "@" of an SSH remote like
// git@github.com:org/repo from being misread as a ref separator.
func splitSpec(spec string) (base, ref string) {
	i := strings.LastIndex(spec, "@")
	if i < 0 || strings.Contains(spec[i+1:], ":") {
		return spec, ""
	}

	return spec[:i], spec[i+1:]
}

// normalizeRepo stores https:// URLs in their canonical scheme-less form
// (spec §10.8); other spellings (SSH, file://) are stored as written.
func normalizeRepo(repo string) string {
	return strings.TrimSuffix(strings.TrimPrefix(repo, "https://"), "/")
}
