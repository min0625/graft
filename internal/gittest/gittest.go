// Copyright 2026 The Graft Authors

// Package gittest creates local bare git repositories under temp dirs for use
// as fixture remotes in integration tests (spec §8), so no test ever touches
// the network.
package gittest

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is a local bare git repository plus a working clone used to author its
// content. Tests hand the bare side (Dir or URL) to graft as the remote.
//
// All helper methods fail the test on any git error.
type Repo struct {
	// Dir is the on-disk path of the bare repository.
	Dir string

	tb      testing.TB
	workDir string
}

// New creates an empty bare repository (default branch "main") and a working
// clone for authoring, both under tb.TempDir(). Commits are authored with a
// fixed identity and ignore the user's git configuration.
func New(tb testing.TB) *Repo {
	tb.Helper()

	base := tb.TempDir()

	repo := &Repo{
		Dir:     filepath.Join(base, "remote.git"),
		tb:      tb,
		workDir: filepath.Join(base, "work"),
	}

	repo.git(base, "init", "--bare", "--initial-branch=main", repo.Dir)
	repo.git(base, "clone", repo.Dir, repo.workDir)

	return repo
}

// URL returns a file:// URL for the bare repository, usable as a remote on
// every platform. The path is percent-encoded because git decodes file://
// URLs — testing.TempDir keeps "%" from test names, so a raw path would
// round-trip wrong.
func (r *Repo) URL() string {
	path := filepath.ToSlash(r.Dir)
	if !strings.HasPrefix(path, "/") {
		// Windows drive paths need the extra slash: file:///C:/...
		path = "/" + path
	}

	u := url.URL{Scheme: "file", Path: path}

	return u.String()
}

// WriteFile writes content to a slash-separated path relative to the working
// clone, creating parent directories. The change reaches the remote on the
// next Commit.
func (r *Repo) WriteFile(path, content string) {
	r.tb.Helper()

	full := filepath.Join(r.workDir, filepath.FromSlash(path))

	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		r.tb.Fatalf("gittest: mkdir for %s: %v", path, err)
	}

	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		r.tb.Fatalf("gittest: write %s: %v", path, err)
	}
}

// Symlink creates a symbolic link in the working directory at linkPath pointing
// to target, then stages it for the next Commit.
func (r *Repo) Symlink(target, linkPath string) {
	r.tb.Helper()

	full := filepath.Join(r.workDir, filepath.FromSlash(linkPath))

	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		r.tb.Fatalf("gittest: mkdir for %s: %v", linkPath, err)
	}

	if err := os.Symlink(target, full); err != nil {
		r.tb.Fatalf("gittest: symlink %s -> %s: %v", linkPath, target, err)
	}

	r.git(r.workDir, "add", filepath.FromSlash(linkPath))
}

// IndexEntry stages a raw tree entry at a slash-separated path directly in
// the index via `git update-index --cacheinfo`, without touching the working
// tree. mode is a git file mode such as "120000" (symlink; content is the
// link target) or "100644" (regular file) — so fixtures can contain entries
// the local filesystem cannot represent: symlinks without symlink privileges
// (Windows), or case-colliding paths on a case-insensitive filesystem.
//
// Because the entry exists only in the index, commit it with CommitIndex —
// Commit's `git add --all` would stage its deletion.
func (r *Repo) IndexEntry(mode, path, content string) {
	r.tb.Helper()

	blob := filepath.Join(r.tb.TempDir(), "blob")
	if err := os.WriteFile(blob, []byte(content), 0o600); err != nil {
		r.tb.Fatalf("gittest: write blob for %s: %v", path, err)
	}

	sha := strings.TrimSpace(r.git(r.workDir, "hash-object", "-w", blob))

	r.git(r.workDir, "update-index", "--add", "--cacheinfo", mode+","+sha+","+path)
}

// GitlinkEntry stages a git-submodule entry (mode 160000) at a
// slash-separated path directly in the index. The recorded commit SHA does
// not need to exist in any repository — git neither validates nor transfers
// gitlink targets. Commit it with CommitIndex (see IndexEntry).
func (r *Repo) GitlinkEntry(path string) {
	r.tb.Helper()

	const submoduleSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	r.git(r.workDir, "update-index", "--add", "--cacheinfo", "160000,"+submoduleSHA+","+path)
}

// CommitIndex commits the index as-is — without Commit's `git add --all`
// re-staging, which would drop index-only entries staged by IndexEntry or
// GitlinkEntry — pushes, and returns the full commit SHA.
func (r *Repo) CommitIndex(message string) string {
	r.tb.Helper()

	r.git(r.workDir, "commit", "--allow-empty", "--message", message)
	r.git(r.workDir, "push", "origin", "HEAD")

	return strings.TrimSpace(r.git(r.workDir, "rev-parse", "HEAD"))
}

// Commit stages all pending changes, commits them on the current branch,
// pushes to the bare repository, and returns the full commit SHA.
func (r *Repo) Commit(message string) string {
	r.tb.Helper()

	r.git(r.workDir, "add", "--all")
	r.git(r.workDir, "commit", "--allow-empty", "--message", message)
	r.git(r.workDir, "push", "origin", "HEAD")

	return strings.TrimSpace(r.git(r.workDir, "rev-parse", "HEAD"))
}

// Tag creates a lightweight tag at the current HEAD and pushes it.
func (r *Repo) Tag(name string) {
	r.tb.Helper()

	r.git(r.workDir, "tag", name)
	r.git(r.workDir, "push", "origin", name)
}

// PackedTag records a lightweight tag at the current HEAD directly in the
// bare repository's packed-refs file, bypassing `git tag`. Loose refs are
// stored as files named after the ref, so a name containing a character the
// host filesystem forbids (e.g. `"` on Windows/NTFS) cannot be created with
// `git tag` — but it is perfectly valid inside packed-refs, which is how such
// a ref reaches a Windows user from a Linux-hosted remote.
//
// The fixture never runs `git pack-refs`, so this only ever creates the file;
// appending to a pre-packed repo would violate its sorted trait.
func (r *Repo) PackedTag(name string) {
	r.tb.Helper()

	sha := strings.TrimSpace(r.git(r.workDir, "rev-parse", "HEAD"))

	f, err := os.OpenFile(filepath.Join(r.Dir, "packed-refs"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		r.tb.Fatalf("gittest: open packed-refs: %v", err)
	}
	defer f.Close() //nolint:errcheck // Write errors surface below.

	if _, err := fmt.Fprintf(f, "%s refs/tags/%s\n", sha, name); err != nil {
		r.tb.Fatalf("gittest: write packed-refs: %v", err)
	}
}

// Branch creates a branch at the current HEAD, pushes it, and switches the
// working clone to it, so subsequent Commit calls land on the new branch.
func (r *Repo) Branch(name string) {
	r.tb.Helper()

	r.git(r.workDir, "switch", "--create", name)
	r.git(r.workDir, "push", "origin", name)
}

// Checkout switches the working clone to an existing branch.
func (r *Repo) Checkout(name string) {
	r.tb.Helper()

	r.git(r.workDir, "switch", name)
}

// Git runs an arbitrary git command in the working clone and returns its
// combined output, as an escape hatch for fixture setups the other helpers do
// not cover.
func (r *Repo) Git(args ...string) string {
	r.tb.Helper()

	return r.git(r.workDir, args...)
}

func (r *Repo) git(dir string, args ...string) string {
	r.tb.Helper()

	cmd := exec.CommandContext(r.tb.Context(), "git", args...) //nolint:gosec // Args come from the test itself.
	cmd.Dir = dir

	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		// Deep-path fixtures exceed Windows' 260-char MAX_PATH; graft forces
		// core.longpaths=true on its own checkouts (spec §5.3), and the
		// fixture's working clone needs the same to author such trees.
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.longpaths",
		"GIT_CONFIG_VALUE_0=true",
		"GIT_AUTHOR_NAME=Graft Test",
		"GIT_AUTHOR_EMAIL=test@graft.invalid",
		"GIT_COMMITTER_NAME=Graft Test",
		"GIT_COMMITTER_EMAIL=test@graft.invalid",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		r.tb.Fatalf("gittest: git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return string(out)
}
