# graft — Design Document

> Status: draft v0.9
> Last updated: 2026-06-13
>
> This is a translation of [`design.zh-TW.md`](design.zh-TW.md); the Chinese version is authoritative if the two ever disagree.

This document describes graft's design and behavioral specification (§1–§9). The decision records (the "why" behind the design) and open questions are out of scope here.

---

## 1. Problem statement

When a git repository needs to depend on files from another git repository — shell scripts, protobuf definitions, CI templates, configuration schemas — every existing option has a clear drawback:

- **git submodule**: poor developer experience, an extra command after every clone, no proper lockfile, confusing state management.
- **git subtree**: merges the dependency's commit history into your repository, has no version manifest, and is hard to update.
- **Gitman**: closest to the ideal, but requires Python 3.10+ — a hard dependency that breaks zero-install CI pipelines.
- **vdm**: a zero-dependency binary, but no lockfile. Pinning to a branch means different machines get different code.

No existing tool provides all of: an intuitive CLI, a lockfile with content verification, and a zero-dependency install.

**Runtime assumption:** graft shells out to the system `git` for all VCS operations (the same way Go's `cmd/go` handles direct VCS fetches). It requires `git` on `$PATH`. Since graft is used inside git repositories, git is in practice always present.

---

## 2. Goals

- **G1** — Provide an npm/cargo-like CLI experience for managing git-repository dependencies.
- **G2** — Ship as a single statically linked binary with no runtime dependency other than the system `git`.
- **G3** — Produce a lockfile that pins both the commit SHA and a content hash, guaranteeing reproducible installs.
- **G4** — Install in parallel to shorten total runtime.
- **G5** — Produce clear, actionable error messages.
- **G6** — Be trivially usable in CI: one line to install, one line to run.
- **G7** — Support all major platforms: macOS, Linux, and Windows.

## 3. File formats

### 3.1 `graft.toml` — the manifest

Edited by humans. Committed to the repository. Defines the desired state.

**Edit-in-place.** `graft add` and `graft remove` modify `graft.toml` in place: only the target `[[dep]]` block is touched (added, updated with specific field changes, or deleted entirely); the rest of the file — the order of other entries, inline and interstitial comments, blank lines — is preserved verbatim. `graft.lock` keeps its existing struct serialization (stable diffs take priority; it is not meant to be hand-edited).

```toml
dir = "deps"                # required — the root directory deps install into, set at `graft init`

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"
```

**Top-level field reference**

| Field | Required | Description |
|-------|----------|-------------|
| `dir` | yes | Root directory for installed dependencies. Defaults to `"deps"` when set by `graft init` (§4.1); a manifest missing this field is a validation error (exit code 2). It must be a relative path inside the repository; `"."` (the repo root) and paths containing `..` are rejected (exit code 2), because the reconcile step deletes any path under `<dir>` that belongs to no dependency. Note: `vendor/` has special meaning in some ecosystems (Go's `go mod vendor`, PHP Composer) — choose a different name if that applies to your project. |

**`[[deps]]` field reference**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Local identifier and install path. Must be unique. Each slash-separated segment must match `[A-Za-z0-9._-]+`. A simple name (`tools`) installs at `<dir>/tools`; a path-like name (`tool-a/util`) installs at `<dir>/tool-a/util`. When `graft add` is run without `--name`, it defaults to the last path segment of `repo` (with any `.git` suffix stripped). The `--name` flag sets the name directly. The name is the entry's primary key; the same `repo` may appear in multiple entries (entry resolution and name-collision rules are in §4.2). Two entries whose names form an ancestor/descendant path (e.g. `foo` and `foo/bar`) conflict because their install paths overlap — that is a validation error (exit code 2). |
| `repo` | yes | A scheme-less repository path (`github.com/org/repo`, fetched over HTTPS), or an explicit `https://` / SSH URL. |
| `version` | yes | The locked version, go.mod-style: a git tag when one exists (`"v1.2.0"`), otherwise a pseudo-version (see below). Written by `graft add`; safe to hand-edit — change it to a new tag and run `graft lock`. The resolved commit SHA lives only in `graft.lock`. A remote re-resolution happens only when a dependency's `repo` or `version` changes — changing only `path` never triggers a ref lookup — so a re-pointed tag can never silently change what gets installed (see §7). |
| `path` | no | A subdirectory of the remote repository to install (e.g. `proto/`). Defaults to the repository root. Lets you take a single directory out of a monorepo without vendoring the whole repository. |

**Pseudo-version.** When a dependency comes from a branch, a raw SHA, or a repository with no tags at all, there is no tag to record, so `graft add` writes a pseudo-version of the form `v0.0.0-20260418091327-a3f8c21d4e8f` — built from the commit's committer timestamp (UTC, `yyyymmddhhmmss`) plus the first 12 characters of the SHA. This matches go.mod's convention for untagged commits: the age is visible at a glance, and it is self-contained — when `graft lock` re-resolves a pseudo-version it reads the embedded SHA directly, with no ref lookup. When resolving `version`, an exact tag match is tried first; only a string that matches the pseudo-version format and is not a tag is parsed as a pseudo-version. Any other tag name (including non-semver tags such as `release-2024`) is accepted as `version` verbatim.

### 3.2 `graft.lock` — the lockfile

Auto-generated by graft. Committed to the repository. Never hand-edited. Defines the actual installed state.

```toml
# This file is auto-generated by graft. Do not edit manually.
# Run `graft lock` to regenerate.

lock_version = 2
dir = "deps"                                               # install root, copied from graft.toml

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"                                        # sync key, copied from graft.toml
commit  = "a3f8c21d4e8f1b2c3d4e5f6a7b8c9d0e1f2a3b4c"  # the SHA this version resolved to
time    = 2026-04-18T09:13:27Z                            # committer timestamp of that commit
hash    = "sha256:e3b0c44298fc1c149afbf4c8996fb924..."  # content hash of the installed tree

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
path    = "proto"
commit  = "b7e1209fa3c8d2e1f0a9b8c7d6e5f4a3b2c1d0e9"
time    = 2026-02-02T18:40:11Z
hash    = "sha256:a665a45920422f9d417e4867efdc4fb8..."
```

**Lockfile field reference**

| Field | Description |
|-------|-------------|
| `lock_version` | Format version, currently fixed at `2` (exec-bit hashing included from day one, see §3.2). Used to detect breaking changes. |
| `dir` | The install root, copied from `graft.toml`. Recorded once at the top level so that `graft apply` can rely on the lockfile alone: each dep's install path is `<dir>/<name>`, which is also how `apply` knows which paths it owns when removing surplus dependencies. |
| `name` | Corresponds to `name` in `graft.toml`. Together with the top-level `dir` it fully determines the install path (`<dir>/<name>`). |
| `repo` | Repository path or URL, copied from `graft.toml`. |
| `version` | The version string, copied verbatim from `graft.toml`. It is the sync key between manifest and lockfile, and lets `status` and `apply` print readable messages offline. |
| `path` | Subdirectory of the remote repository, copied from `graft.toml`. Omitted when unset. |
| `commit` | The full 40-character commit SHA that `version` resolved to at lock time. The only field `apply` relies on when installing. |
| `time` | The committer timestamp of `commit` (TOML datetime, UTC). Purely informational — lets a reader see at a glance how old a locked dependency is. The committer timestamp is controlled by the upstream author, so it is never used for verification. |
| `hash` | The SHA-256 content hash of the installed file tree (see below). |

**Hash computation**

The `hash` field is computed as:
`sha256(sort(sha256(filepath + "\n" + exec_byte + content) for each file in the tree))`

Concretely: for each file in the installed tree, compute `sha256` over the UTF-8 file path, a newline, one exec byte (`\x00` for non-executable, `\x01` for executable), and the raw file bytes; sort all the resulting hex strings alphabetically; then compute the final `sha256` over their concatenation. This lets `graft apply` and `graft status` detect any difference between the locked content and what is actually on disk — including a hand-edited vendor file or a script whose executable bit was stripped — and report it explicitly.

Normalization rules ensure the same file tree hashes identically on every platform:

- File paths are relative to the dependency's install root and always use forward slashes (`/`), even on Windows.
- The `.git` directory is removed after checkout and is never included in the hash or the installed tree.
- File content is hashed as raw bytes — no newline conversion. graft forces `core.autocrlf=false` and `core.eol=lf` on its own clones, so even files marked `text` by an upstream `.gitattributes` check out byte-for-byte identically on every platform.
- Symlinks are unsupported: if the fetched file tree (after `path` filtering) contains a symlink, the install fails with exit code 2. Symlinks cannot be created reliably on Windows, and how to hash them (the link target string vs. the followed content) would make the result platform-dependent.
- **The executable bit is part of the hash**: each file's hash input includes one exec byte (`\x00` non-executable, `\x01` executable) immediately after the path and newline, before the file content. The exec bit is determined from the git object database mode (`100755` vs `100644`), not from the filesystem after checkout, so the same commit hashes identically on POSIX and Windows. graft explicitly applies exec bits from the git index after checkout; `store.Materialize` preserves them when copying. A change that touches only the exec bit — e.g. `chmod -x` on a script — is reported as `modified` by `graft status` and rejected by `graft apply` with exit code 4. Unsupported modes (e.g. `160000` git submodule or `120000` symlink) are rejected with exit code 2.
- File paths must be representable on all supported platforms: paths containing newlines, characters Windows disallows (`< > : " \ | ? *`, control characters), or Windows reserved names (`CON`, `NUL`, etc.) are rejected with exit code 2. Rejecting newlines also keeps the `filepath + "\n" + exec_byte + content` hash input unambiguous.
- Empty directories are not tracked by git, are never installed, and do not participate in the hash — an extra empty directory in vendor is not drift.
- A fetched file tree (after `path` filtering) that contains no files at all is rejected with exit code 2 — an empty dependency is almost always a typo in `path`, and rejecting it also keeps the install and hash semantics from degenerating.
- **Cross-platform path collision detection**: if the fetched file tree contains two paths that are identical after Unicode case-folding (e.g. `Foo.txt` and `foo.txt`) or after Unicode normalization (NFC vs NFD), the install fails with exit code 2 and names the conflicting paths. Case-insensitive filesystems (macOS APFS and Windows NTFS by default) would overwrite one with the other at checkout, breaking the G3 reproducible-install guarantee.

---

## 4. Command design

### 4.1 Command reference

| Command | Modifies toml | Modifies lock | Modifies vendor | Description |
|--------|--------------|---------------|-----------------|-------------|
| `init` | creates | — | — | Create `graft.toml` (`graft init [dir]`) |
| `add` | yes | yes | yes | Add or update a dependency (ref optional, defaults to resolving the latest tag) |
| `remove` | yes | yes | yes | Remove a dependency |
| `apply` | no | no | yes | Reconcile vendor to the state defined by the lockfile: add missing, remove surplus, align versions (CI-friendly) |
| `lock` | no | yes | no | Re-sync the lockfile from `graft.toml`. New entries, and entries whose `repo` or `version` changed, are re-resolved and fetched to a temp directory to compute the content hash — but nothing is installed. Entries whose `repo` and `version` are both unchanged keep their locked commit (no network); when only `path` changed, the locked commit is re-fetched to recompute `hash`, with no ref lookup |
| `lock --check` | no | no | no | Verify that `graft.lock` is already the up-to-date resolution of `graft.toml` **without writing any files**; consistent → exit 0, needs re-resolution → exit 2 with a list of out-of-date entries (§4.3) |
| `status` | no | no | no | Read-only report of the manifest ↔ lockfile ↔ vendor sync state |
| `cache` | — | — | — | Inspect or clean the global cache (`dir`, `verify`, `clean`); never touches project files |

`graft init [dir]` creates `graft.toml` in the current directory. The optional argument sets the install root; it defaults to `"deps"` when omitted. It fails with exit code 2 when `graft.toml` already exists — it never silently overwrites.

**Project root discovery.** Except for `init` and `cache`, every command walks upward from the current working directory to the nearest directory containing a `graft.toml` — the project root — and runs as if invoked there: relative paths resolve against the project root (`dir` is project-root relative; each dependency installs at `<dir>/<name>`). The upward walk never crosses a git repository boundary (the directory containing `.git` is the last one tried). When no `graft.toml` is found, the command fails with exit code 2 and the hint: `graft.toml not found. Run 'graft init' first.`

`graft remove <name>` fails with exit code 2 when the name does not exist in `graft.toml`, consistent with the other manifest validation errors.

### 4.2 `graft add` semantics

`graft add` is the only command for declaring a dependency, and it does double duty for both adding and changing versions — there is no separate "update" command.

```
graft add <repo>[@ref] [--name <name>] [--path <dir>]
```

The first argument is always a repository path. When updating an existing dependency, any `--name` or `--path` flag that is passed replaces the existing value; a flag that is not passed keeps the original. To rename a dependency, remove it and re-add it with the new name.

**`--name` flag.** Sets the dep's `name` (and therefore its install path under `<dir>`). The full value — including any `/` — becomes the entry name. A simple name (`--name tools`) installs at `<dir>/tools`; a path-like name (`--name tool-a/util`) installs at `<dir>/tool-a/util`.

**Entry resolution.** Which entry in `graft.toml` `add` operates on is decided by the following rules. These are all manifest-level validations that happen before any network access; a violation fails with exit code 2:

- With `--name` → target the entry whose name exactly matches the `--name` value: update it if it exists (the repo argument becomes the new `repo` — giving both a name and a repo explicitly is treated as a deliberate re-point); add it if it does not.
- Without `--name` → match existing entries by normalized repo (canonical form, see §5.6):
  - exactly one entry → update that entry, keeping its name (even a custom one).
  - more than one entry (the same repo declared multiple times) → error: list the matching names, suggesting `--name` to disambiguate.
  - no entry → add it with the derived default name; if that name is already taken by an entry pointing at a **different repo** → error, suggesting `--name`. `add` never silently re-points an existing entry to another repo because of a name coincidence.

**The same repo declared multiple times.** The manifest allows the same `repo` in multiple entries — the typical case is taking several subdirectories of a monorepo, each with a different `path`. Each entry is resolved and locked independently keyed by `name` (and may be locked at a different version), and they share the same cached bare repository (§5.6). Adding a second entry requires `--name` with an unused name — otherwise, by the rules above, the repo match would land on the first entry and update it instead. Subsequent version updates are done by passing the repo URL again (with `--name` when multiple entries share the repo).

**The ref argument:**

- **Tag** (`@v1.2.0`): the tag name is written as `version` in `graft.toml`; its commit SHA is resolved against the remote and locked in `graft.lock`.
- **Branch or full/partial SHA** (`@main`, `@a3f8c21d`): resolved against the remote to a full 40-character commit SHA. Because there is no tag to record, `graft.toml` gets a pseudo-version (`v0.0.0-<timestamp>-<sha12>`, see §3.1) and `graft.lock` locks the full SHA. The install always uses the locked commit, so a later branch move cannot change what is installed.
- **`@latest` or omitted**: graft fetches the remote's tag list and, among all tags matching the SemVer 2.0 format (an optional `v` prefix is allowed, so both `v1.2.0` and `1.2.0` match), picks the highest version. Pre-release tags (e.g. `v2.0.0-rc.1`) are skipped. If no tag matches, it falls back to the remote `HEAD` and writes a pseudo-version.
- **Precedence:** when one ref name is both a tag and a branch, it resolves as a tag — consistent with git's own refname resolution order. A tag literally named `latest` is shadowed by the special meaning of `@latest` (the same trade-off as go.mod); this is a documented limitation.
- Running `graft add repo@main` again later may lock a newer commit — that is exactly the deliberate "update" action, and it shows up as a one-line `version` diff in `graft.toml`.
- A remote that is reachable but the ref does not exist is a general error (exit code 1); a remote that cannot be reached at all is a network error (exit code 3).

**Behavior when the dependency already exists:**

- Resolves to the same commit → no-op, prints `✓ shared-scripts already at a3f8c21 (v1.2.0)`.
- A different commit → update `version` in `graft.toml`, rewrite the matching entry in `graft.lock`, reinstall into vendor.

After rewriting its own entry in `graft.toml`, `add` finishes with full `graft lock` semantics — it re-syncs *every* entry, not just the one it changed, so hand-edits to other dependencies are handled in the same run — and then runs the same reconcile as `graft apply`: the vendor directory is brought to exactly the state of the lockfile — missing dependencies added, surplus removed, mismatched aligned. As a result `add` never fails because of a pre-existing toml ↔ lock mismatch; it resolves that mismatch directly.

### 4.3 `lock --check` semantics

**`graft lock --check`**
- Offline-only mode: uses string comparison to verify that `graft.lock` is the up-to-date resolution of `graft.toml`.
- Writes no files and makes no network requests.
- If `graft.lock` does not exist → exits with code 2 and: `graft.lock not found. Run 'graft lock' first.`
- For each manifest entry it compares the matching lockfile entry's `repo`, `version`, and `path`; for each locked entry it confirms the name still appears in the manifest. Any mismatch (addition, removal, or field change) → exits with code 2, listing each out-of-date dependency name and prompting `graft lock` before committing.
- If everything matches → exits with code 0 and prints: `✓ graft.lock is up to date`
- Typical CI usage: run `graft lock --check` to ensure the lockfile is committed in sync with the manifest before proceeding.

### 4.4 `apply` semantics

**`graft apply`**
- Reads `graft.lock` only, ignoring `graft.toml` for version resolution.
- Aligns the vendor directory to the state defined by the lockfile: adds missing dependencies, removes surplus ones, upgrades or downgrades version-mismatched ones.
- If `graft.lock` does not exist → exits with code 2 and: `graft.lock not found. Run 'graft lock' first.`
- If `graft.lock` is out of sync with `graft.toml` (a dependency exists in only one of them, or a dependency's `version`, `repo`, `path`, or resolved `dest` in `graft.toml` differs from what `graft.lock` records) → exits with code 2 and: `graft.toml and graft.lock are out of sync. Run 'graft lock' to update the lockfile.` This check is a pure string comparison — no network.
- If the vendor directory content matches the lockfile hash → skipped (no-op, prints `✓ already up to date`).
- Never modifies `graft.toml` or `graft.lock`.

### 4.5 `status` semantics

**`graft status`**
- Read-only: modifies no files and makes no network access.
- If `graft.lock` does not exist, every dependency in `graft.toml` is reported as `out of sync` (exit code 1).
- For each dependency it reports one of the following states:
  - `ok` — present in toml, lock, and vendor; vendor content matches the locked hash.
  - `missing` — locked but not present in vendor.
  - `modified` — present in vendor but content does not match the locked hash.
  - `out of sync` — toml and lock disagree (the dependency exists on only one side, or `version`/`repo`/`path`/`dest` differ).
  - `extra` — a path under `<dir>` that belongs to no locked dependency (a leftover from a removed dependency, or created by hand). `graft apply` will delete it. A toml ↔ lock disagreement is always reported as `out of sync`, never `extra`. When the lockfile does not exist, `extra` is not reported — everything is already `out of sync`, and an extra report would just be noise.
- A dependency present only in lock and not in toml is likewise reported as `out of sync`.
- Output is an aligned table, one row per `✓/✗ <name>  <short commit> (<version>)  <state>`; rows with no trustworthy lock information (`out of sync` and `extra`) replace the commit column with `-`. For example:

  ```
  ✓ shared-scripts  a3f8c21 (v1.2.0)  ok
  ✗ proto-defs      b7e1209 (v0.8.1)  modified
  ```

- In link mode (§5.6), the vendor check inspects the link target: pointing at `store/<locked hash>` is `ok`, a wrong target is `modified`, a dangling link is `missing`. `graft status --deep` additionally re-hashes the pointed-to store entry.
- Exits with code 0 when everything is `ok`; exits with code 1 when any drift is detected. This lets `graft status` serve as a low-cost CI gate (for example, verifying that a committed `vendor/` has not been hand-edited) without changing anything.

### 4.6 Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error (written to stderr) |
| 2 | Config / lockfile validation error |
| 3 | Network error |
| 4 | Hash mismatch (content integrity failure) |

Distinct exit codes let CI pipelines tell "network outage" apart from "vendor content was tampered with".

`graft status` also reports drift with exit code 1 (see §4.5) — for status, any non-zero exit code simply means "not clean".

### 4.7 `graft cache`

The global cache (§5.6) is invisible in normal use; the following subcommands manage it:

- `graft cache dir` — print the cache directory path.
- `graft cache verify` — re-hash every store entry; report and delete corrupted entries (exit code 4 if any are found).
- `graft cache clean` — remove store entries with no registered link-mode dest referencing them, plus bare repositories not fetched recently; `--all` deletes the entire cache. Cleaning may break other projects' link-mode vendors: affected dependencies show as `missing` in `graft status`, and `graft apply` will re-materialize them (re-fetching if necessary).

---

## 5. Architecture

### 5.1 Package structure

```
graft/
├── cmd/
│   └── graft/              # CLI layer (package main + cobra commands)
│       ├── main.go
│       ├── root.go
│       ├── project.go      # shared helpers: open project, reconcile, output
│       ├── relock.go       # shared `graft lock` re-sync logic (used by lock and add)
│       ├── init.go
│       ├── add.go
│       ├── remove.go
│       ├── apply.go
│       ├── lock.go
│       ├── status.go
│       └── cache.go        # `graft cache` subcommands (dir / verify / clean)
└── internal/
    ├── clierr/             # exit codes (§4.6) + error output format (§6)
    │   ├── clierr.go
    │   └── clierr_test.go
    ├── config/             # graft.toml read/write
    │   ├── manifest.go
    │   └── manifest_test.go
    ├── lockfile/           # graft.lock read/write
    │   ├── lock.go
    │   └── lock_test.go
    ├── gitrun/             # git execution layer shared by resolver/fetcher:
    │   └── gitrun.go       #   run git, fetch helpers, network error classification
    ├── resolver/           # ref resolution (tag/branch/partial SHA → full SHA)
    │   ├── resolver.go
    │   └── resolver_test.go
    ├── fetcher/            # git clone/fetch + sparse checkout
    │   ├── fetcher.go
    │   └── fetcher_test.go
    ├── hasher/             # content hash computation
    │   ├── hasher.go
    │   └── hasher_test.go
    ├── vendordir/          # vendor directory management (not named vendor/: some Go
    │   ├── vendordir.go     #   toolchains special-case a directory named vendor, see §3.1)
    │   └── vendordir_test.go
    ├── cachedir/           # cache directory path resolution (Dir, Repos, Store, …)
    │   └── cachedir.go
    ├── repocache/          # per-repo bare-repo cache: EnsureCommit, Checkout, lock
    │   └── repocache.go
    ├── store/              # content-addressed store: Insert, Exists, Path (§5.6)
    │   └── store.go
    ├── links/              # link-mode dest registry for `cache clean` (§5.6)
    │   └── links.go
    ├── projlock/           # per-project advisory lock for state-modifying commands (§5.7)
    │   └── projlock.go
    └── gittest/            # bare-repo fixture remotes for integration tests (§8)
        ├── gittest.go
        └── gittest_test.go
```

### 5.2 Core data types

```go
// Manifest represents graft.toml
type Manifest struct {
    Dir    string `toml:"dir"`    // required; set at `graft init`
    Deps   []Dep  `toml:"deps"`
}

type Dep struct {
    Name    string `toml:"name"`
    Repo    string `toml:"repo"`
    Version string `toml:"version"`        // git tag, or a pseudo-version for an untagged commit
    Path    string `toml:"path,omitempty"` // optional: subdirectory of the remote repository
}

// Lockfile represents graft.lock
type Lockfile struct {
    LockVersion int          `toml:"lock_version"`
    Dir         string       `toml:"dir"`    // install root, copied from graft.toml
    Deps        []LockedDep  `toml:"deps"`
}

type LockedDep struct {
    Name    string    `toml:"name"`
    Repo    string    `toml:"repo"`
    Version string    `toml:"version"`        // sync key, copied verbatim from graft.toml
    Path    string    `toml:"path,omitempty"` // subdirectory of the remote repository
    Commit  string    `toml:"commit"`         // full SHA the version resolved to at lock time
    Time    time.Time `toml:"time"`           // committer timestamp of that commit (UTC)
    Hash    string    `toml:"hash"`           // sha256 of the content tree
}
```

### 5.3 Apply flow

```
graft apply
     │
     ▼
load graft.lock
     │
     ├─ missing → exit 2
     │
     ▼
verify lock ↔ toml consistency
     │
     ├─ out of sync → exit 2
     │
     ▼
for each dependency (parallel, N workers):
     │
     ▼
  check <dest> against the lockfile
  (copy mode: content hash; link mode: link target)
     │
     ├─ matches → skip (already installed)
     │
     ▼
  store/<hash> exists? ──── yes ─────────────┐
     │ no                                    │
     ▼                                       │
  ensure <commit> is in the bare-repo cache  │
  (incremental fetch; fallback in 5.5)       │
     │                                       │
     ▼                                       │
  check <commit> out to <cache>/tmp/,        │
  remove .git (with <path> set, sparse       │
  checkout limits the working tree: see 5.5) │
     │                                       │
     ▼                                       │
  compute the content hash                   │
     │                                       │
     ├─ mismatch → exit 4 (integrity failure)│
     │                                       │
     ▼                                       │
  atomic rename into store/<hash>            │
  (files made read-only)                     │
     │◄──────────────────────────────────────┘
     ▼
  materialize store/<hash> into <dest>
  (copy mode: reflink/copy staged under <dir>/.graft-tmp then rename;
   link mode: create a symlink / junction)
     │
     ▼
remove items under <dir> matching no locked dest
     │
     ▼
print summary
```

The checkout staging area lives in `<cache>/tmp/`, on the same filesystem as the store, so the rename into `store/` is atomic; if another process is building the same entry concurrently, the loser of the rename race simply uses the existing entry. Copy-mode materialization is staged under `<dir>/.graft-tmp/` rather than the system temp directory — so that the final move into `<dest>` is an atomic same-filesystem rename. Leftover items from an interrupted run in either staging area are cleaned up on the next state-modifying command, and `.graft-tmp` is never treated as a surplus dependency during reconcile.

### 5.4 Parallelism

Installs are split into two phases, each with its own worker pool:

1. **Fetch phase** (network-bound): worker count is `min(numDeps, fetchJobs)`. `fetchJobs` defaults to **16** and can be overridden with the `--jobs <n>` flag or the `GRAFT_CONCURRENCY` environment variable. Each worker ensures its dep's tree is present in the content store: a store hit returns immediately; otherwise the dep is fetched, hashed, and inserted into the store.
2. **Install phase** (CPU/IO-bound): worker count is `min(numDeps, installJobs)`. `installJobs` defaults to `runtime.NumCPU()` when `--jobs` is unset; when `--jobs` is specified it equals the fetch phase count. Each worker atomically moves the already-prepared store tree into `<dest>` via rename (or copy-on-write reflink).

Both phases collect all errors and report them together after the phase completes (no fail-fast, so you see every error at once).

`--jobs <n>` (or the equivalent `GRAFT_CONCURRENCY=<n>`) sets the concurrency cap for both phases. `--jobs 1` forces fully sequential execution. Multiple deps from the same bare repository are still serialized by the per-repo advisory lock (§5.6) even when the fetch phase worker count is greater than one.

### 5.5 Fetch strategy

All fetches target that dependency's bare cache repository (§5.6) and are incremental — a commit already in the cache is never re-fetched. Fetching directly by commit SHA (`git fetch <repo> <commit>`) only works when the server allows it (`uploadpack.allowReachableSHA1InWant` or `allowAnySHA1InWant`). GitHub, GitLab, and Gitea all enable it; a plain `git daemon` or an older server may not. So graft tries, in order:

1. `git fetch --depth=1 <repo> <commit>` — cheapest; supported by all major hosts.
2. If the server rejects the SHA and the locked entry's `version` is a tag: `git fetch --depth=1 <repo> <tag>`, then check whether `FETCH_HEAD` is exactly `commit`. A mismatch here is not an error — the tag may have been re-pointed — and graft continues to the next step.
3. Fetch all refs in full, then check out `commit` — always correct, but the most bandwidth-expensive.

If the commit cannot be obtained at all, graft distinguishes two causes in the error message: a network failure (exit code 3), or "that commit no longer exists on the remote" (e.g. history was rewritten), suggesting `graft add <name>@<ref>` to re-lock.

For a dependency with `path` set, the fetch additionally passes `--filter=blob:none` and configures a sparse checkout of `<path>`, so blobs outside the target subdirectory are never downloaded. When the server does not support partial clone, graft silently falls back to a normal fetch — the sparse checkout still limits the working tree to `<path>`. Note that filter-excluded blobs are downloaded on demand at checkout, so offline materialization of a `path` dependency is only guaranteed once its file tree is in the content store.

**No Git LFS support in v1.** A plain `git` checkout by graft materializes LFS pointer files, and the lockfile hash would silently lock the pointer rather than the real content. If the checked-out tree (after `path` filtering) contains a `.gitattributes` declaring an `lfs` filter, the install fails with exit code 2 and an error message naming the dependency — an explicit, documented limitation rather than a silent trap.

### 5.6 Global cache and content store

All downloads flow through a user-level cache (default: the OS user cache directory, e.g. `~/.cache/graft` on Linux, `~/Library/Caches/graft` on macOS, `%LocalAppData%\graft\cache` on Windows; overridable with `GRAFT_CACHE_DIR`):

```
<cache>/
├── repos/<host>/<org>/<repo>.git   # bare repos, incrementally fetched, shared across projects
├── store/sha256/<xx>/<hex…>/       # immutable file-tree snapshots, keyed by lockfile content hash
├── links/                          # registry of link-mode dests (queried by `cache clean`)
├── tmp/                            # checkout staging area (same filesystem as store)
└── locks/                          # advisory locks: per-repo fetch lock + per-project modify lock (§5.7)
```

**Bare-repo cache.** The key is always the canonical `<host>/<org>/<repo>` form (scheme, userinfo, and the `.git` suffix stripped), regardless of how `repo` is written — `https://github.com/org/repo`, `github.com/org/repo`, and `git@github.com:org/repo.git` all share one entry. One advisory file lock per repository serializes concurrent fetches into the same bare repo; the rest of the cache is lock-free via atomic renames. Any commit ever fetched can be reinstalled offline.

**Content store.** A store entry is the complete installed tree for a given lockfile `hash`: checked out to `tmp/`, hashed, verified, then atomically renamed into place with all files made read-only. Because the key *is* the hash recorded in `graft.lock`, a store hit needs neither network nor re-hashing. Two benefits fall out naturally: `graft lock` fills the store while it computes the hash, so the following `graft apply` installs with no re-download; and content that is byte-for-byte identical — even from different repos or versions — is stored only once per machine.

**Materialization.** How a store entry becomes `<dest>`:

- **copy** (default) — uses copy-on-write reflink when the filesystem supports it (APFS, btrfs, XFS, ReFS), otherwise a plain copy. Observable behavior is exactly the same as graft without a cache, including the commit-`vendor/` workflow, and `apply` still re-verifies the vendor tree's hash on every run.
- **link** (opt-in: `graft apply --link` or `GRAFT_LINK_MODE=symlink`) — `<dest>` becomes a single directory symlink pointing at the store (a junction on Windows, requiring no admin privileges), registered in `links/`. Any number of projects share one on-disk file tree. Verification reduces to a cheap link-target comparison: pointing at `store/<locked hash>` is `ok`, a wrong target is `modified`, a dangling link is `missing`; `graft status --deep` additionally re-hashes the store entry itself. Limitations: `vendor/` must be gitignored (committing a link is meaningless to other machines), and vendor integrity then rests on the store's immutability — files are read-only, so an accidental edit through the link fails immediately. This mode is a machine-local choice, never recorded in `graft.toml` or `graft.lock`; during reconcile, a dest found materialized in the other mode is treated as drift and rewritten in the current mode.

The cache is purely a performance layer: deleting the entire cache is always safe, and no lockfile guarantee depends on it. GC and inspection are provided by `graft cache` (§4.7).

### 5.7 Concurrency and locking

How mature package managers handle concurrent execution:

- **go** — serializes downloads into the shared module cache with a single advisory lock file (`$GOMODCACHE/cache/lock`); extracted cache entries are created atomically and kept read-only; `go.mod` / `go.sum` are written through a locked file.
- **Cargo** — an advisory lock on the global package cache (`$CARGO_HOME/.package-cache`) plus a per-project build-directory lock; a second cargo process blocks and prints `Blocking waiting for file lock on package cache` until the first finishes.
- **uv** — advisory file locks scoped to the resource being modified (a cache shard, a target environment); concurrent commands wait rather than error.

The common pattern: protect the global cache with an advisory file lock plus atomic, immutable entries; serialize project-level modifications with one lock per project; make waiters block with a message rather than error. graft follows the same pattern:

- **Cache side** (§5.6): a per-repo fetch lock; store entries are immutable and created by atomic rename — no whole-cache global lock needed.
- **Project side:** every state-modifying command (`add`, `remove`, `apply`, `lock`) takes that project's mutual-exclusion advisory lock before reading `graft.toml`, and holds it until vendor and the lockfile are consistent again. The lock file lives in the global cache — `<cache>/locks/projects/<sha256 of the resolved directory path of graft.toml>` — not in the repository: no gitignore needed, impossible to commit by accident, and fully compatible with the commit-`vendor/` workflow. A second graft process blocks rather than fails, printing `waiting for another graft process to finish…` if it waits more than a second.
- `graft status` and `graft cache` do not take the project lock. `status` is read-only; running it concurrently with `apply` may report transient drift — the same caveat as running `git status` during a rebase.

---

## 6. Error message design

Following Cargo's philosophy: every error should tell the user what went wrong, and what to do next.

```
# lockfile out of sync
error: graft.lock is out of sync with graft.toml

  dependency "new-scripts" is in graft.toml but not in graft.lock

  run `graft lock` to update the lockfile, then commit it

# hash mismatch
error: content integrity check failed for "shared-scripts"

  expected  sha256:e3b0c44298fc1c149afbf4c8996fb924...
  got       sha256:a665a45920422f9d417e4867efdc4fb8...

  the installed content does not match what was locked — usually a
  hand-edited vendor directory or a manually altered lockfile
  run `graft apply` after restoring the lockfile, or `graft add shared-scripts@<ref>`
  to deliberately re-pin and re-lock

# network failure
error: could not clone "shared-scripts"

  repo:   https://github.com/your-org/shared-scripts
  reason: connection refused

  check your network connection and that the repo URL is correct
```

---

## 7. Security considerations

**Ref mutability.** Branches move and tags can be force-pushed, so what graft installs is never resolved from a ref at install time: `graft apply` reads only `commit` from the lockfile. The manifest's `version` is resolved against the remote exactly once — only when a dependency is new, or when its `repo`/`version` changes. An unchanged `(repo, version)` pair keeps the locked commit, so a re-pointed tag cannot silently change what is installed; an untagged lock uses a pseudo-version with the commit SHA embedded, requiring no ref lookup at all. The only remaining trust point is the first resolution (and a re-resolution after deleting the lockfile) — the same as Cargo, and equivalent to the go.mod trust model without a sumdb. The content hash in `graft.lock` additionally guards the installed file tree itself: a hand-edited `vendor/` or a tampered lockfile makes `graft apply` fail with exit code 4 (shown as `modified` in `graft status`).

**No arbitrary code execution.** Dependencies are static file trees; graft never executes anything within them.

**Path safety.** `dir` must be a relative path inside the repository; `name` names a path under `<dir>` (and `path` selects a subdirectory of the remote repo) — absolute paths and `..` segments are rejected at the validation stage (exit code 2); the fully-resolved install path (`<dir>/<name>`) always lands inside the install tree, so a malicious or corrupt manifest/lockfile can never direct an install, or a reconcile delete, outside it. Within the fetched file tree, git itself refuses to track paths containing `..` or `.git`, so a malicious dependency cannot escape its own install root either.

**Shared cache.** The cache (§5.6) is user-level and in the same trust domain as the projects that use it. Every store entry is hash-verified at creation and kept read-only; `graft cache verify` can re-check all entries at any time, and in link mode `graft status --deep` can audit the entry a vendor actually points at. In copy mode, `apply` re-verifies the vendor tree on every run, exactly as without a cache.

**HTTPS by default.** A scheme-less repository path (`github.com/org/repo`) is fetched over HTTPS. SSH is also supported but must be written out explicitly (`git@github.com:org/repo.git`). Because graft invokes external `git`, a user-level `url.<base>.insteadOf` rewrite (for example, forcing a host to use SSH) takes effect automatically.

---

## 8. Testing strategy

**Unit tests** cover all pure functions: config parsing, lockfile parsing, hash computation, version-string parsing.

**Integration tests** use local bare git repositories as fixture remotes, created on the fly under a temp directory by the `internal/gittest` helper. No network access is needed. Each test creates a temporary working directory, runs graft commands as subprocesses, and verifies file content and exit codes.

**Golden file tests** for CLI output — stdout/stderr is verified for each command to catch regressions in error messages.

---

## 9. v1 scope and milestone plan

### Milestone 1 — core install loop
- `graft init [dir]`
- `graft add` (no sparse checkout yet)
- `graft apply`
- `graft lock`
- lockfile with commit SHA + content hash

### Milestone 2 — full CLI
- `graft remove`
- `graft status`
- `@latest` / omitted-ref resolution and branch-ref support for `graft add`
- `path` subdirectory support (sparse checkout)

### Milestone 3 — polish
- parallel installs (worker pool)
- state-modifying commands take a per-project advisory lock (§5.7)
- clear error messages on all error paths
- install script (`curl | sh`)
- Homebrew formula

### Milestone 4 — caching and deduplication
- global bare-repo cache + content-addressed store (copy mode, reflink where supported)
- `graft cache dir` / `verify` / `clean`
- opt-in link mode (symlink / junction dest, `links/` registry)

### Milestone 5 — ecosystem
- GitHub Actions example
- GitLab CI example
- documentation site
