// Copyright 2026 The Graft Authors

package main

import (
	"context"
	"fmt"
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
		Use:   "add <repo>[@ref] | <name>[@ref]",
		Short: "Add or update a dependency",
		Long: `Add a dependency, or change the version of an existing one — there is no
separate update command. The ref may be a tag, a branch, or a full or
partial commit SHA; tags are recorded as the version, anything untagged
becomes a pseudo-version, and the resolved commit is pinned in graft.lock.
Omitting the ref or using "@latest" selects the highest non-pre-release
SemVer tag, or the remote HEAD when no suitable tag exists.

For a dependency that already exists in graft.toml the first argument may be
its name instead of the repo, e.g. "graft add shared-scripts@v1.3.0".
When updating an existing dependency, flags that are passed replace the
stored values; omitted flags keep them.

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
			opts.destSet = cmd.Flags().Changed("dest")
			opts.pathSet = cmd.Flags().Changed("path")

			// Trim a trailing slash so `--path proto/` matches how config.Load
			// normalizes the same value read from graft.toml.
			opts.path = strings.TrimSuffix(opts.path, "/")

			return runAdd(cmd, args[0], opts)
		},
	}

	cmd.Flags().StringVar(&opts.name, "name", "",
		"local identifier for the dependency (default: last repo path segment)")
	cmd.Flags().StringVar(&opts.dest, "dest", "",
		"where to place the dependency locally (default: <vendor>/<name>)")
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
		name := opts.name
		if name == "" {
			name = config.DefaultName(repo)
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

	if _, err := p.reconcile(ctx, next, out, linkMode(false), concurrencyJobs(cmd)); err != nil {
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

// addOpts carries the --name/--dest/--path flags of graft add. The *Set
// fields record whether each flag was passed at all: when updating an
// existing dependency, passed flags replace the stored values and omitted
// flags keep them (spec §4.2). For --dest and --path, passing an empty value
// (--dest "") resets the field to its default; --name has no empty form, since
// ValidateName rejects "" with exit code 2.
type addOpts struct {
	name, dest, path          string
	nameSet, destSet, pathSet bool
}

// validate rejects invalid flag values before any network access.
func (o addOpts) validate() error {
	if o.nameSet {
		if err := config.ValidateName(o.name); err != nil {
			return err
		}
	}

	if o.destSet && o.dest != "" {
		if err := config.ValidatePath("--dest", o.dest); err != nil {
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

	if o.destSet {
		dep.Dest = o.dest
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

// targetDep decides which manifest entry the add targets: a base containing
// "/" is a repo path (normalized; the dep may be new), anything else must be
// the name of an existing dep (spec §4.2 — names can never contain "/").
// A repo-form base without --name is matched against existing entries by
// canonical repo (§10.8): exactly one match updates it (keeping its name), several
// matches are an error naming them, and no match adds a new entry — unless
// the derived default name is taken by a different repo, which is an error
// rather than a silent re-point. A nil dep with a nil error means "add new".
func targetDep(m *config.Manifest, base string, opts addOpts) (dep *config.Dep, repo string, err error) {
	if !strings.Contains(base, "/") {
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

	repo = normalizeRepo(base)

	// Giving both the name and the repo is a deliberate re-point: the named
	// entry is updated if it exists, added otherwise.
	if opts.nameSet {
		return m.FindDep(opts.name), repo, nil
	}

	// Match on the canonical <host>/<org>/<repo> form so the same remote
	// written as HTTPS, scheme-less, or scp-like SSH (and with or without a
	// ".git" suffix) is recognised as one entry (spec §4.2, §10.8). The stored
	// value keeps its normalized-but-as-written form (repo).
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

		return nil, "", clierr.New(clierr.CodeConfig,
			fmt.Sprintf("%s is declared by multiple entries: %s", repo, strings.Join(names, ", ")),
			"use `graft add <name>@<ref>` or pass --name to say which entry to update",
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
