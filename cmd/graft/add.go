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
	"github.com/min0625/graft/internal/vendordir"
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
			opts.subdirSet = cmd.Flags().Changed("subdir")
			opts.symlinksSet = cmd.Flags().Changed("symlinks")

			// Trim a trailing slash so `--subdir proto/` matches how config.Load
			// normalizes the same value read from graft.toml.
			opts.subdir = strings.TrimSuffix(opts.subdir, "/")

			return runAdd(cmd, args[0], opts)
		},
	}

	cmd.Flags().StringVar(&opts.name, "name", "",
		"dep name, also its install path under the vendor directory (e.g. tools or nested/tools)")
	cmd.Flags().StringVar(&opts.subdir, "subdir", "",
		"subdirectory of the remote repo to install (default: repo root)")
	cmd.Flags().StringVar(&opts.symlinks, "symlinks", "",
		`symlink policy: "reject" (default) or "skip" (sets symlinks in graft.toml)`)

	return cmd
}

func runAdd(cmd *cobra.Command, spec string, opts addOpts) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	base, ref, hasRef := splitSpec(spec)

	if base == "" {
		return clierr.New(clierr.CodeConfig,
			"graft add requires a repository path",
			"example: graft add github.com/org/shared-scripts@v1.2.0",
		)
	}

	// A trailing "@" with nothing after it is almost always a typo. Omitting the
	// "@" entirely is how you ask for @latest; an empty ref is rejected so the
	// two cannot be confused.
	if hasRef && ref == "" {
		return clierr.New(clierr.CodeConfig,
			fmt.Sprintf("empty ref in %q", spec),
			`omit the "@" for @latest, or give a ref like @v1.2.0`,
		)
	}

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

	mode, err := resolveMode()
	if err != nil {
		return err
	}

	result, err := p.reconcile(ctx, next, mode)
	if err != nil {
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

	// add re-syncs every entry, not just the targeted one (spec §4.2). Flag any
	// resulting changes to other deps — a hand-edited version picked up, or
	// vendor drift repaired — so they aren't mistaken for the requested change.
	if hasCollateral(result, dep.Name) {
		printf(out, "also synced other dependencies:\n")
	}

	printReconcile(out, result, dep.Name, "")

	return nil
}

// hasCollateral reports whether the whole-lockfile re-sync (spec §4.2) touched
// any dep other than the one add targeted — an install or an upgrade of another
// entry, or a removal — so add can flag those lines instead of letting them read
// as part of the requested change.
func hasCollateral(r *vendordir.Result, target string) bool {
	if len(r.Removed) > 0 {
		return true
	}

	for _, d := range r.Installed {
		if d.Name != target {
			return true
		}
	}

	return false
}

// addOpts carries the --name/--subdir/--symlinks flags of graft add. The *Set fields record
// whether each flag was passed at all: when updating an existing dependency,
// passed flags replace the stored values and omitted flags keep them (spec §4.2).
// --name accepts a dep name, optionally slash-separated to also set the install
// path under the vendor directory (e.g. "tools" → name=tools; "nested/tools" →
// name=nested/tools, installed at <vendor>/nested/tools). Passing --subdir ""
// resets the subdir to the repo root.
type addOpts struct {
	name, subdir       string
	nameSet, subdirSet bool
	symlinks           string
	symlinksSet        bool
}

// validate rejects invalid flag values before any network access.
func (o addOpts) validate() error {
	if o.nameSet {
		if err := config.ValidateName(o.name); err != nil {
			return err
		}
	}

	if o.subdirSet && o.subdir != "" {
		if err := config.ValidatePath("--subdir", o.subdir); err != nil {
			return err
		}
	}

	if o.symlinksSet && o.symlinks != config.SymlinksReject && o.symlinks != config.SymlinksSkip {
		return clierr.New(clierr.CodeConfig,
			fmt.Sprintf("invalid --symlinks %q", o.symlinks),
			`--symlinks must be "reject" or "skip"`,
		)
	}

	return nil
}

func (o addOpts) apply(dep *config.Dep) {
	if o.nameSet {
		dep.Name = o.name
	}

	if o.subdirSet {
		dep.Subdir = o.subdir
	}

	if o.symlinksSet {
		// reject is the default; store it as the empty string so passing
		// --symlinks=reject drops the key from graft.toml rather than writing
		// a redundant `symlinks = "reject"`. Omitting the flag keeps the
		// existing value (sticky), like --name/--subdir.
		if o.symlinks == config.SymlinksReject {
			dep.Symlinks = ""
		} else {
			dep.Symlinks = o.symlinks
		}
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
// it is updated in place — but only if that entry already tracks this repo;
// pointing an existing name at a different repository is rejected (spec §4.2).
// Without --name the repo is matched against existing entries by canonical URL
// (spec §4.2, §10.8): exactly one match updates it (keeping its name), several
// matches are an error, and no match adds a new entry — unless the derived
// default name is taken by a different repo. A nil dep with a nil error means
// "add new".
func targetDep(m *config.Manifest, base string, opts addOpts) (dep *config.Dep, repo string, err error) {
	repo = normalizeRepo(base)

	// Canonical <host>/<org>/<repo> form so the same remote written as HTTPS,
	// scheme-less, or scp-like SSH (and with or without a ".git" suffix) is
	// recognised as one entry (spec §4.2, §10.8).
	key := gitrun.CanonicalRepo(base)

	// --name explicitly names the entry to update or create. It may update an
	// existing entry that already tracks this repo, but never re-points an
	// existing name to a different repository.
	if opts.nameSet {
		d := m.FindDep(opts.name)
		if d != nil && gitrun.CanonicalRepo(d.Repo) != key {
			return nil, "", clierr.New(clierr.CodeConfig,
				fmt.Sprintf("dependency name %q is already taken by an entry for %s", opts.name, d.Repo),
				"add never silently re-points an existing entry to another repository",
				fmt.Sprintf("remove %q first, or choose a different --name", opts.name),
			)
		}

		return d, repo, nil
	}

	var matches []*config.Dep

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

// splitSpec splits <base>@<ref>. hasRef reports whether an "@" ref separator
// was present (so a trailing "@" with an empty ref can be told apart from no
// "@" at all). The candidate ref must not contain ":" — git forbids it in
// refnames — which keeps the "@" of an SSH remote like git@github.com:org/repo
// from being misread as a ref separator.
func splitSpec(spec string) (base, ref string, hasRef bool) {
	i := strings.LastIndex(spec, "@")
	if i < 0 || strings.Contains(spec[i+1:], ":") {
		return spec, "", false
	}

	return spec[:i], spec[i+1:], true
}

// normalizeRepo stores https:// URLs in their canonical scheme-less form
// (spec §10.8); other spellings (SSH, file://) are stored as written.
func normalizeRepo(repo string) string {
	repo = strings.TrimSuffix(strings.TrimPrefix(repo, "https://"), "/")

	// Drop a trailing ".git" only for the web-hosted (scheme-less / HTTPS)
	// form, where it is optional. SSH (git@host:path), ssh:// and file://
	// remotes keep theirs — there ".git" can be a real path segment, and the
	// presence of "@" or ":" tells those forms apart from a bare host/org/repo.
	if !strings.ContainsAny(repo, "@:") {
		repo = strings.TrimSuffix(repo, ".git")
	}

	return repo
}
