// Copyright 2026 The Graft Authors

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/config"
	"github.com/min0625/graft/internal/gitrun"
	"github.com/min0625/graft/internal/lockfile"
	"github.com/min0625/graft/internal/resolver"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var opts addOpts

	cmd := &cobra.Command{
		Use:   "add <repo>[@ref]",
		Short: "Add or update a dependency",
		Long: `Add a dependency, or change the version of an existing one — there is no
separate update command. The ref may be a tag, a branch, or a full or
partial commit SHA; tags are recorded as the version, anything untagged
becomes a pseudo-version, and the resolved commit is pinned in graft.lock.
Omitting the ref or using "@latest" selects the highest non-pre-release
SemVer tag, or the remote HEAD when no suitable tag exists.

The first argument is always the repository path. When an existing entry
tracks that repo, it is updated in place; graft never silently re-points
an entry to a different repository.

After updating its entry, add re-syncs the whole lockfile (like graft lock)
and reconciles the vendor directory (like graft apply).`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return clierr.New(clierr.CodeConfig,
					"graft add requires exactly one <repo>[@ref] argument",
					"example: graft add github.com/org/shared-scripts@v1.2.0",
				)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.nameSet = cmd.Flags().Changed("name")
			opts.pathSet = cmd.Flags().Changed("path")

			// Trim a trailing slash so `--path proto/` matches how config.Load
			// normalizes the same value read from graft.toml.
			opts.path = strings.TrimSuffix(opts.path, "/")

			return runAdd(cmd, args[0], opts)
		},
	}

	cmd.Flags().StringVar(&opts.name, "name", "",
		"dep name and install path under vendor (e.g. tools or nested/tools)")
	cmd.Flags().StringVar(&opts.path, "path", "",
		"subdirectory of the remote repo to install (default: repo root)")

	return cmd
}

func runAdd(cmd *cobra.Command, spec string, opts addOpts) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	base, ref := splitSpec(spec)

	if err := opts.validate(); err != nil {
		return err
	}

	p, release, err := openProjectLocked(cmd)
	if err != nil {
		return err
	}
	defer release()

	dep, repo, err := targetDep(p.manifest, base, opts)
	if err != nil {
		return err
	}

	isNew := dep == nil
	if isNew {
		name := config.DefaultName(repo)
		if opts.nameSet {
			name = opts.name
		}

		p.manifest.Deps = append(p.manifest.Deps, config.Dep{Name: name})
		dep = &p.manifest.Deps[len(p.manifest.Deps)-1]
	}

	before := *dep
	dep.Repo = repo
	opts.apply(dep)

	res, version, err := resolveAddRef(ctx, repo, ref)
	if err != nil {
		return err
	}

	prev, found, err := p.loadLock()
	if err != nil {
		return err
	}

	if !found {
		prev = lockfile.New()
	}

	// The same locked commit is a no-op for the entry — keep the stored
	// version string — unless a flag changed the entry anyway (spec §4.2).
	already := false

	if !isNew {
		if ld := prev.FindDep(before.Name); ld != nil && ld.Repo == repo && ld.Commit == res.Commit {
			version = ld.Version
			already = *dep == before
		}
	}

	dep.Version = version

	next, err := relock(ctx, p.manifest, prev, map[string]resolver.Resolution{dep.Name: res})
	if err != nil {
		return err
	}

	if err := p.manifest.Validate(); err != nil {
		return err
	}

	if isNew {
		if err := config.AppendDep(p.manifestPath(), *dep); err != nil {
			return err
		}
	} else {
		if err := config.UpdateDep(p.manifestPath(), *dep); err != nil {
			return err
		}
	}

	if err := next.Write(p.lockPath()); err != nil {
		return err
	}

	mode, err := resolveMode("")
	if err != nil {
		return err
	}

	if _, err := p.reconcile(ctx, next, out, mode); err != nil {
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

// addOpts carries the --name/--path flags of graft add. The *Set fields record
// whether each flag was passed at all: when updating an existing dependency,
// passed flags replace the stored values and omitted flags keep them (spec §4.2).
// --name accepts a dep name, optionally slash-separated to also set the install
// path under vendor (e.g. "tools" → name=tools; "nested/tools" → name=nested/tools,
// installed at <vendor>/nested/tools). Passing --path "" resets the path to the
// repo root.
type addOpts struct {
	name, path       string
	nameSet, pathSet bool
}

// validate rejects invalid flag values before any network access.
func (o addOpts) validate() error {
	if o.nameSet {
		if err := config.ValidateName(o.name); err != nil {
			return err
		}
	}

	if o.pathSet && o.path != "" {
		if err := config.ValidatePath("--path", o.path); err != nil {
			return err
		}
	}

	return nil
}

func (o addOpts) apply(dep *config.Dep) {
	if o.nameSet {
		dep.Name = o.name
	}

	if o.pathSet {
		dep.Path = o.path
	}
}

// resolveAddRef resolves the ref from a `graft add` invocation and returns the
// Resolution plus the version string to store in the manifest. An empty or
// "latest" ref triggers @latest resolution.
func resolveAddRef(ctx context.Context, repo, ref string) (resolver.Resolution, string, error) {
	if ref == "" || ref == "latest" {
		return resolveLatestRef(ctx, repo)
	}

	res, err := resolver.ResolveRef(ctx, repo, ref)
	if err != nil {
		return resolver.Resolution{}, "", err
	}

	version := resolver.PseudoVersion(res.Time, res.Commit)
	if res.IsTag {
		version = ref
	}

	return res, version, nil
}

func resolveLatestRef(ctx context.Context, repo string) (resolver.Resolution, string, error) {
	res, tag, err := resolver.ResolveLatest(ctx, repo)
	if err != nil {
		return resolver.Resolution{}, "", err
	}

	if tag != "" {
		res.IsTag = true

		return res, tag, nil
	}

	return res, resolver.PseudoVersion(res.Time, res.Commit), nil
}

// targetDep decides which manifest entry the add targets. The first argument
// is always a repository path (repo form). When --name names an existing entry
// that entry is updated or re-pointed; otherwise the repo is matched against
// existing entries by canonical URL (spec §4.2, §10.8): exactly one match
// updates it (keeping its name), several matches are an error, and no match
// adds a new entry — unless the derived default name is taken by a different
// repo. A nil dep with a nil error means "add new".
func targetDep(m *config.Manifest, base string, opts addOpts) (dep *config.Dep, repo string, err error) {
	repo = normalizeRepo(base)

	// --name explicitly names the entry to update or create.
	if opts.nameSet {
		return m.FindDep(opts.name), repo, nil
	}

	// Match on the canonical <host>/<org>/<repo> form so the same remote
	// written as HTTPS, scheme-less, or scp-like SSH (and with or without a
	// ".git" suffix) is recognised as one entry (spec §4.2, §10.8).
	var matches []*config.Dep

	key := gitrun.CanonicalRepo(base)

	for i := range m.Deps {
		if gitrun.CanonicalRepo(m.Deps[i].Repo) == key {
			matches = append(matches, &m.Deps[i])
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], repo, nil
	case 0:
		name := config.DefaultName(repo)
		if taken := m.FindDep(name); taken != nil {
			return nil, "", clierr.New(clierr.CodeConfig,
				fmt.Sprintf("dependency name %q is already taken by an entry for %s", name, taken.Repo),
				"add never silently re-points an existing entry to another repository",
				"pass --name <name> to add this repository under a different name",
			)
		}

		return nil, repo, nil
	default:
		names := make([]string, len(matches))
		for i, d := range matches {
			names[i] = d.Name
		}

		slices.Sort(names)

		return nil, "", clierr.New(clierr.CodeConfig,
			fmt.Sprintf("%s is declared by multiple entries: %s", repo, strings.Join(names, ", ")),
			"pass --name <name> to say which entry to update",
		)
	}
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
