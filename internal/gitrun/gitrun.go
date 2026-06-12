// Copyright 2026 The Graft Authors

// Package gitrun runs the system git for the resolver and fetcher packages:
// command execution, repo-path-to-URL mapping, the shared fetch helpers, and
// network error classification (spec §4.5, §5.5).
package gitrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/min0625/graft/internal/clierr"
)

// scpLikeRe matches SSH remotes of the scp-like form git@host:path.
var scpLikeRe = regexp.MustCompile(`^[^/@]+@[^/:]+:`)

// RemoteURL maps a manifest repo value to the URL git fetches from:
// scheme-less paths (github.com/org/repo) become HTTPS; explicit URLs and
// scp-like SSH remotes are used as written (spec §3.1, §7).
func RemoteURL(repo string) string {
	if strings.Contains(repo, "://") || scpLikeRe.MatchString(repo) {
		return repo
	}

	return "https://" + repo
}

// Run executes git with the given arguments in dir (the process working
// directory when dir is empty) and returns its stdout. A failure returns an
// error carrying git's stderr.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // Args are built from validated manifest values.
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}

		return "", fmt.Errorf("git %s: %s", args[0], reason)
	}

	return stdout.String(), nil
}

// NetworkErr wraps a failed remote operation as an exit-3 network error in
// the spec §6 format.
func NetworkErr(repo string, err error) error {
	return clierr.New(clierr.CodeNetwork,
		fmt.Sprintf("could not reach %q", repo),
		"reason: "+err.Error(),
		"check your network connection and that the repo URL is correct",
	)
}

// Reachable reports whether the remote answers at all, used to tell a
// network failure (exit 3) from a missing commit (exit 1).
func Reachable(ctx context.Context, repo string) bool {
	_, err := Run(ctx, "", "ls-remote", "--quiet", "--", RemoteURL(repo), "HEAD")

	return err == nil
}

// FetchSHA fetches a single commit at depth 1 into the repository at dir —
// the cheap path, which the server may reject for unadvertised SHAs
// (spec §5.5).
func FetchSHA(ctx context.Context, dir, repo, sha string) error {
	_, err := Run(ctx, dir, "fetch", "--quiet", "--depth=1", "--", RemoteURL(repo), sha)

	return err
}

// FetchAll fetches every branch and tag from the remote into the repository
// at dir — the always-correct fallback of spec §5.5. A failure is classified
// as a network error when the remote is unreachable.
func FetchAll(ctx context.Context, dir, repo string) error {
	// Branches land under refs/remotes so the fetch can never collide with a
	// checked-out branch in the throwaway repository.
	_, err := Run(ctx, dir, "fetch", "--quiet", "--", RemoteURL(repo),
		"+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*")
	if err == nil {
		return nil
	}

	if !Reachable(ctx, repo) {
		return NetworkErr(repo, err)
	}

	return err
}

// CommitTime reads the committer timestamp of a commit available in the
// repository at dir, in UTC.
func CommitTime(ctx context.Context, dir, sha string) (time.Time, error) {
	out, err := Run(ctx, dir, "show", "--no-patch", "--format=%cI", sha)
	if err != nil {
		return time.Time{}, fmt.Errorf("read committer time: %w", err)
	}

	t, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse committer time: %w", err)
	}

	return t.UTC(), nil
}

// RemoveAll removes path like os.RemoveAll, additionally clearing read-only
// permissions when the first attempt fails — git object files are read-only,
// which os.RemoveAll cannot delete on Windows.
func RemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	//nolint:errcheck,gosec // Best-effort permission fixing; the retry reports failures.
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // Keep walking what is reachable.
		}

		if d.IsDir() {
			os.Chmod(p, 0o700) //nolint:errcheck,gosec // Best effort.
		} else {
			os.Chmod(p, 0o600) //nolint:errcheck,gosec // Best effort.
		}

		return nil
	})

	return os.RemoveAll(path)
}
