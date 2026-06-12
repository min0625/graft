// Copyright 2026 The Graft Authors

// Package resolver resolves a manifest version or a `graft add` ref — a tag,
// branch, or full/partial commit SHA — against a remote repository to a full
// 40-character commit SHA plus its committer timestamp, by shelling out to
// the system git (spec §3.1, §4.2).
package resolver

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/min0625/graft/internal/clierr"
	"github.com/min0625/graft/internal/gitrun"
)

// Resolution is the result of resolving a ref or version against a remote.
type Resolution struct {
	// Commit is the full 40-character SHA the ref resolved to.
	Commit string
	// Time is the committer timestamp of Commit, in UTC.
	Time time.Time
	// IsTag reports whether the ref resolved as a tag, in which case the tag
	// name itself is the manifest version.
	IsTag bool
}

const pseudoTimeFormat = "20060102150405"

var (
	pseudoRe = regexp.MustCompile(`^v0\.0\.0-(\d{14})-([0-9a-f]{12})$`)
	hexRe    = regexp.MustCompile(`^[0-9a-f]{4,40}$`)
)

// PseudoVersion formats a go.mod-style pseudo-version for an untagged
// commit: v0.0.0-<yyyymmddhhmmss>-<sha12>.
func PseudoVersion(t time.Time, commit string) string {
	return "v0.0.0-" + t.UTC().Format(pseudoTimeFormat) + "-" + commit[:12]
}

// ParsePseudoVersion extracts the embedded commit SHA prefix and timestamp
// from a pseudo-version. ok is false when v is not a pseudo-version.
func ParsePseudoVersion(v string) (t time.Time, sha12 string, ok bool) {
	m := pseudoRe.FindStringSubmatch(v)
	if m == nil {
		return time.Time{}, "", false
	}

	t, err := time.Parse(pseudoTimeFormat, m[1])
	if err != nil {
		return time.Time{}, "", false
	}

	return t.UTC(), m[2], true
}

// ResolveVersion resolves a manifest version string (spec §3.1): an exact
// tag match is tried first; a string matching the pseudo-version pattern is
// parsed as one (the embedded SHA is looked up, no ref lookup); any other
// string is treated as a tag name verbatim. A version that resolves to
// nothing is exit 1; an unreachable remote is exit 3.
func ResolveVersion(ctx context.Context, repo, version string) (Resolution, error) {
	if res, found, err := lookupTag(ctx, repo, version); err != nil || found {
		return res, err
	}

	if _, sha12, ok := ParsePseudoVersion(version); ok {
		return resolveCommit(ctx, repo, sha12)
	}

	return Resolution{}, clierr.New(clierr.CodeGeneral,
		fmt.Sprintf("version %q not found in %s", version, repo),
		"no tag with that name exists on the remote",
		"fix the version in graft.toml, or run `graft add <repo>@<ref>` to re-pin",
	)
}

// ResolveRef resolves a `graft add` ref (spec §4.2): a tag wins over a
// branch with the same name, and anything else that looks like a commit SHA
// or pseudo-version is resolved as a commit. A nonexistent ref is exit 1; an
// unreachable remote is exit 3.
func ResolveRef(ctx context.Context, repo, ref string) (Resolution, error) {
	if res, found, err := lookupTag(ctx, repo, ref); err != nil || found {
		return res, err
	}

	if res, found, err := lookupBranch(ctx, repo, ref); err != nil || found {
		return res, err
	}

	if _, sha12, ok := ParsePseudoVersion(ref); ok {
		return resolveCommit(ctx, repo, sha12)
	}

	if hexRe.MatchString(ref) {
		return resolveCommit(ctx, repo, ref)
	}

	return Resolution{}, clierr.New(clierr.CodeGeneral,
		fmt.Sprintf("ref %q not found in %s", ref, repo),
		"no tag or branch with that name exists on the remote, and it does not look like a commit SHA",
	)
}

// lookupTag resolves ref as a tag via ls-remote, preferring the peeled
// commit of an annotated tag.
func lookupTag(ctx context.Context, repo, ref string) (Resolution, bool, error) {
	refs, err := lsRemote(ctx, repo, "refs/tags/"+ref, "refs/tags/"+ref+"^{}")
	if err != nil {
		return Resolution{}, false, err
	}

	sha, ok := refs["refs/tags/"+ref+"^{}"]
	if !ok {
		if sha, ok = refs["refs/tags/"+ref]; !ok {
			return Resolution{}, false, nil
		}
	}

	res, err := resolveCommit(ctx, repo, sha)
	if err != nil {
		return Resolution{}, false, err
	}

	res.IsTag = true

	return res, true, nil
}

// lookupBranch resolves ref as a branch head via ls-remote.
func lookupBranch(ctx context.Context, repo, ref string) (Resolution, bool, error) {
	refs, err := lsRemote(ctx, repo, "refs/heads/"+ref)
	if err != nil {
		return Resolution{}, false, err
	}

	sha, ok := refs["refs/heads/"+ref]
	if !ok {
		return Resolution{}, false, nil
	}

	res, err := resolveCommit(ctx, repo, sha)
	if err != nil {
		return Resolution{}, false, err
	}

	return res, true, nil
}

// lsRemote lists matching refs on the remote as a refname → SHA map. Any
// ls-remote failure is reported as a network error (exit 3).
func lsRemote(ctx context.Context, repo string, patterns ...string) (map[string]string, error) {
	args := append([]string{"ls-remote", "--quiet", "--", gitrun.RemoteURL(repo)}, patterns...)

	out, err := gitrun.Run(ctx, "", args...)
	if err != nil {
		return nil, gitrun.NetworkErr(repo, err)
	}

	refs := make(map[string]string)

	for line := range strings.Lines(out) {
		sha, ref, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "\t")
		if ok {
			refs[ref] = sha
		}
	}

	return refs, nil
}

// resolveCommit resolves a full or partial commit SHA against the remote and
// reads its committer time, by fetching into a throwaway repository: a full
// SHA is tried as a cheap depth-1 fetch first; otherwise (and as a fallback)
// all refs are fetched and the SHA is expanded locally.
func resolveCommit(ctx context.Context, repo, sha string) (Resolution, error) {
	dir, err := os.MkdirTemp("", "graft-resolve-*")
	if err != nil {
		return Resolution{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer gitrun.RemoveAll(dir) //nolint:errcheck // Best-effort temp cleanup.

	if _, err := gitrun.Run(ctx, "", "init", "--quiet", dir); err != nil {
		return Resolution{}, fmt.Errorf("git init: %w", err)
	}

	full := sha

	fetched := len(sha) == 40 && gitrun.FetchSHA(ctx, dir, repo, sha) == nil
	if !fetched {
		if err := gitrun.FetchAll(ctx, dir, repo); err != nil {
			return Resolution{}, err
		}

		full, err = gitrun.Run(ctx, dir, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
		if err != nil {
			return Resolution{}, clierr.New(clierr.CodeGeneral,
				fmt.Sprintf("commit %q not found in %s", sha, repo),
				"the commit does not exist on the remote — it may have been garbage-collected or the history rewritten",
				"run `graft add <name>@<ref>` to re-pin the dependency",
			)
		}

		full = strings.TrimSpace(full)
	}

	t, err := gitrun.CommitTime(ctx, dir, full)
	if err != nil {
		return Resolution{}, err
	}

	return Resolution{Commit: full, Time: t}, nil
}
