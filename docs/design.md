# Graft — Design Document

> Status: draft v0.9
> Last updated: 2026-07-03
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

**Edit-in-place, kept sorted by `name`.** `graft add` and `graft remove` modify `graft.toml` in place: only the target `[[dep]]` block's specific fields are rewritten (or a block is added or deleted entirely); the hand-written field formatting, inline and interstitial comments, and blank lines of every other entry are preserved verbatim. After the write the file's `[[dep]]` blocks are re-sorted by `name`, each block carrying the comment immediately above it (its preamble) along with it — the same behavior as `go.mod`: human-readable and -writable, comments glued to their entry, yet entries always sorted. The only trade-off is a group-header comment spanning several entries, which can end up detached from what it described once sorting reorders the group. Content before the first `[[dep]]` (the header and `dir`) is left untouched by the sort. `graft.lock` is likewise sorted by `name` and keeps its struct serialization (stable diffs take priority; it is not meant to be hand-edited).

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
| `dir` | yes | Root directory for installed dependencies. Defaults to `"deps"` when set by `graft init` (§4.1); a manifest missing this field is a validation error (exit code 2). It must be a relative path inside the repository; `"."` (the repo root), paths containing `..`, and paths containing a `.git` segment (e.g. `.git`, `vendor/.git`) are rejected (exit code 2), because the reconcile step deletes any path under `<dir>` that belongs to no dependency — a `<dir>` overlapping `.git` would destroy the repository. Note: `vendor/` has special meaning in some ecosystems (Go's `go mod vendor`, PHP Composer) — choose a different name if that applies to your project. |

**`[[deps]]` field reference**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Local identifier and install path. Must be unique. Each slash-separated segment must match `[A-Za-z0-9._-]+`. A simple name (`tools`) installs at `<dir>/tools`; a path-like name (`tool-a/util`) installs at `<dir>/tool-a/util`. When `graft add` is run without `--name`, it defaults to the last path segment of `repo` (with any `.git` suffix stripped). The `--name` flag sets the name directly. The name is the entry's primary key; the same `repo` may appear in multiple entries (entry resolution and name-collision rules are in §4.2). Two entries whose names form an ancestor/descendant path (e.g. `foo` and `foo/bar`) conflict because their install paths overlap — that is a validation error (exit code 2). A name's first segment must also not start with `.graft-` (matched case-insensitively, so a variant like `.GRAFT-TMP` that would collide on a case-insensitive filesystem is rejected on every platform): that prefix is reserved for graft's internal directories under the vendor root, which are always named `.graft-<something>` (e.g. the reconcile staging directory `.graft-tmp`), so such a name would collide with one of them and could never apply — also a validation error (exit code 2). A `.`-bearing name such as `github.com/org/repo` is unaffected (its first segment is `github.com`), and so is a bare `.graft` without the trailing hyphen. |
| `repo` | yes | A scheme-less repository path (`github.com/org/repo`, fetched over HTTPS), or an explicit `https://` / SSH URL. |
| `version` | yes | The locked version, go.mod-style: a git tag when one exists (`"v1.2.0"`), otherwise a pseudo-version (see below). Written by `graft add`. Safe to hand-edit **for tags** — change it to a new tag and run `graft lock`. **Pseudo-versions are derived** (they embed a committer timestamp and a 12-character SHA prefix) and cannot be hand-calculated; to change one, re-run `graft add` rather than editing it directly. The resolved commit SHA lives only in `graft.lock`. A remote re-resolution happens only when a dependency's `repo` or `version` changes — changing only `subdir` never triggers a ref lookup — so a re-pointed tag can never silently change what gets installed (see §7). |
| `subdir` | no | A subdirectory of the remote repository to install (e.g. `proto/`). Defaults to the repository root. Lets you take a single directory out of a monorepo without vendoring the whole repository. |
| `symlinks` | no | Symlink-handling policy, a string enum: `"reject"` (default, may be omitted) or `"skip"`. When `"skip"`, silently skips all symlinks in the dependency tree — they are excluded from the hash and not copied to the vendor directory — and prints a per-symlink warning when the dependency is added or re-locked (`graft add` / `graft lock`). Intended for upstream repos that contain incidental symlinks (e.g. doc links, compat aliases) that the vendor consumer does not need. Defaults to `"reject"` (symlinks are rejected with exit code 2). It is a string enum rather than a boolean because a boolean name (`allow-symlinks`) would be misleading, and to leave room for future finer-grained symlink policies. |

**Pseudo-version.** When a dependency comes from a branch, a raw SHA, or a repository with no tags at all, there is no tag to record, so `graft add` writes a pseudo-version of the form `v0.0.0-20260418091327-a3f8c21d4e8f` — built from the commit's committer timestamp (UTC, `yyyymmddhhmmss`) plus the first 12 characters of the SHA. This matches go.mod's convention for untagged commits: the age is visible at a glance, and it is self-contained — when `graft lock` re-resolves a pseudo-version it reads the embedded SHA directly, with no ref lookup. When resolving `version`, an exact tag match is tried first; only a string that matches the pseudo-version format and is not a tag is parsed as a pseudo-version. Any other tag name (including non-semver tags such as `release-2024`) is accepted as `version` verbatim.

### 3.2 `graft.lock` — the lockfile

Auto-generated by graft. Committed to the repository. Never hand-edited. Defines the actual installed state.

```toml
# This file is auto-generated by graft. Do not edit manually.
# Run `graft lock` to regenerate.

lock_version = 1
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
subdir  = "proto"
commit  = "b7e1209fa3c8d2e1f0a9b8c7d6e5f4a3b2c1d0e9"
time    = 2026-02-02T18:40:11Z
hash    = "sha256:a665a45920422f9d417e4867efdc4fb8..."
```

**Lockfile field reference**

| Field | Description |
|-------|-------------|
| `lock_version` | Format version, currently fixed at `1`. Used to detect breaking changes. |
| `dir` | The install root, copied from `graft.toml`. Recorded once at the top level so that `graft apply` can rely on the lockfile alone: each dep's install path is `<dir>/<name>`, which is also how `apply` knows which paths it owns when removing surplus dependencies. |
| `name` | Corresponds to `name` in `graft.toml`. Together with the top-level `dir` it fully determines the install path (`<dir>/<name>`). |
| `repo` | Repository path or URL, copied from `graft.toml`. |
| `version` | The version string, copied verbatim from `graft.toml`. It is the sync key between manifest and lockfile, and lets `status` and `apply` print readable messages offline. |
| `subdir` | Subdirectory of the remote repository, copied from `graft.toml`. Omitted when unset. |
| `symlinks` | The symlink policy, copied from `graft.toml`. Omitted when unset (the default `reject`). It is part of the manifest↔lockfile sync check: changing the policy without re-locking is reported as out of sync by `apply`, so the result never depends on whether the content store is warm. |
| `commit` | The full commit SHA that `version` resolved to at lock time (a non-empty hex string; SHA-1 is 40 characters, SHA-256 is 64 characters). The only field `apply` relies on when installing. |
| `time` | The committer timestamp of `commit` (TOML datetime, UTC). Purely informational — lets a reader see at a glance how old a locked dependency is. The committer timestamp is controlled by the upstream author, so it is never used for verification. |
| `hash` | The SHA-256 content hash of the installed file tree (see below). |

**Canonical field order.** Within each `[[deps]]` block the fields must appear in this order: `name`, `repo`, `version`, `subdir` (omitted when unset), `symlinks` (omitted when unset), `commit`, `time`, `hash`. Tools that write `graft.lock` must follow this order to produce stable diffs.

**Load-time validation.** When reading `graft.lock`, graft validates every entry before any install or hashing: `name`, `repo`, and `version` must be non-empty, `commit` must be 40 or 64 hex digits, and `hash` must be `sha256:` followed by 64 lowercase hex digits. Any violation fails with exit code 2 (configuration error) and a hint to re-run `graft lock` — so a truncated or hand-edited lockfile never reaches the content store with a malformed hash.

**Hash computation**

The `hash` field is computed as:
`sha256(sort(sha256(filepath + "\n" + exec_byte + content) for each file in the tree))`

Concretely: for each file in the installed tree, compute `sha256` over the UTF-8 file path, a newline, one exec byte (`\x00` for non-executable, `\x01` for executable), and the raw file bytes, encoding the result as 64 lowercase hex characters; sort all the resulting per-file hex strings by ASCII byte order (lexicographic on the 64-character lowercase hex string); then compute the final `sha256` over their concatenation, also encoded as 64 lowercase hex characters. The `hash` field stores the result as `sha256:` followed by those 64 characters (e.g. `sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`); the `sha256:` prefix is part of the stored value. This lets `graft apply` and `graft status` detect any difference between the locked content and the vendored file contents — for example a hand-edited vendor file — and report it explicitly. (The exec byte is taken from recorded git metadata rather than the live filesystem mode; see below.)

Normalization rules ensure the same file tree hashes identically on every platform:

- File paths are relative to the dependency's install root and always use forward slashes (`/`), even on Windows. When `subdir` is set, only files under that subdirectory are included, and their paths in the hash have the `<subdir>/` prefix stripped — matching the paths they appear at in the vendor directory.
- The `.git` directory is removed after checkout and is never included in the hash or the installed tree.
- File content is hashed as raw bytes — no newline conversion. graft forces `core.autocrlf=false` and `core.eol=lf` on its own clones, so even files marked `text` by an upstream `.gitattributes` check out byte-for-byte identically on every platform.
- Symlinks are rejected by default with exit code 2; the error message names the specific symlink path. Symlinks cannot be created reliably on Windows, and how to hash them (the link target string vs. the followed content) would make the result platform-dependent. **Opt-in skip**: setting `symlinks = "skip"` on a dependency in `graft.toml` causes graft to silently skip all symlinks — they are excluded from the hash and not copied to the vendor directory — and print a per-symlink warning when the dependency is added or re-locked (`graft add` / `graft lock`). The vendor directory remains symlink-free and the reproducible-install guarantee is preserved. `symlinks` is a string enum rather than a boolean: the boolean `allow-symlinks = true` would be misleading (it reads as "keep the symlinks" when it actually strips them), whereas the enum is self-documenting and leaves room for future finer-grained symlink policies.
- **The executable bit is part of the hash**: each file's hash input includes one exec byte (`\x00` non-executable, `\x01` executable) immediately after the path and newline, before the file content. The exec bit is determined from the git object database mode (`100755` vs `100644`), not from the filesystem after checkout, so the same commit hashes identically on POSIX and Windows, with the bit preserved during materialization. An *upstream* exec-bit change — a different git mode, picked up on the next `graft lock` — changes the hash and shows up as `modified` / is rejected by `graft apply` with exit code 4. A *local* `chmod` on an already-vendored file is deliberately invisible: the live filesystem mode never feeds the hash. Unsupported modes (e.g. `160000` git submodule or `120000` symlink) are rejected with exit code 2.
- File paths must be representable on all supported platforms: paths containing newlines, characters Windows disallows (`< > : " \ | ? *`, control characters), or Windows reserved names (`CON`, `NUL`, etc.) are rejected with exit code 2. Rejecting newlines also keeps the `filepath + "\n" + exec_byte + content` hash input unambiguous.
- Empty directories are not tracked by git, are never installed, and do not participate in the hash — an extra empty directory in vendor is not drift.
- A fetched file tree (after `subdir` filtering) that contains no files at all is rejected with exit code 2 — an empty dependency is almost always a typo in `subdir`, and rejecting it also keeps the install and hash semantics from degenerating.
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
| `lock` | no | yes | no | Re-sync the lockfile from `graft.toml`. New entries, and entries whose `repo` or `version` changed, are re-resolved and fetched to a temp directory to compute the content hash — but nothing is installed. Entries whose `repo` and `version` are both unchanged keep their locked commit (no network); when only `subdir` or `symlinks` changed, the locked commit is re-fetched to recompute `hash`, with no ref lookup |
| `lock --check` | no | no | no | Verify that `graft.lock` is already the up-to-date resolution of `graft.toml` **without writing any files**; consistent → exit 0, needs re-resolution → exit 2 with a list of out-of-date entries (§4.3) |
| `status` | no | no | no | Read-only report of the manifest ↔ lockfile ↔ vendor sync state |
| `cache` | — | — | — | Inspect or prune the global cache (`dir`, `verify`, `prune`, `clean`); never touches project files |

`graft init [dir]` creates `graft.toml` in the current directory. The optional argument sets the install root; it defaults to `"deps"` when omitted. It fails with exit code 2 when `graft.toml` already exists — it never silently overwrites.

**Project root discovery.** Except for `init` and `cache`, every command walks upward from the current working directory to the nearest directory containing a `graft.toml` — the project root — and runs as if invoked there: relative paths resolve against the project root (`dir` is project-root relative; each dependency installs at `<dir>/<name>`). The upward walk never crosses a git repository boundary (the directory containing `.git` is the last one tried). When no `graft.toml` is found, the command fails with exit code 2 and the hint: `graft.toml not found. Run 'graft init' first.`

`graft remove <name>` fails with exit code 2 when the name does not exist in `graft.toml`, consistent with the other manifest validation errors.

### 4.2 `graft add` semantics

`graft add` is the only command for declaring a dependency, and it does double duty for both adding and changing versions — there is no separate "update" command.

```
graft add <repo>[@ref] [--name <name>] [--subdir <dir>] [--symlinks <reject|skip>]
```

The first argument is always a repository path. When updating an existing dependency, any `--name`, `--subdir`, or `--symlinks` flag that is passed replaces the existing value; a flag that is not passed keeps the original. To rename a dependency, remove it and re-add it with the new name.

**`--name` flag.** Sets the dep's `name` (and therefore its install path under `<dir>`). The full value — including any `/` — becomes the entry name. A simple name (`--name tools`) installs at `<dir>/tools`; a path-like name (`--name tool-a/util`) installs at `<dir>/tool-a/util`.

**`--symlinks` flag.** Sets `symlinks` (`reject` or `skip`) on the entry. Because `graft add` finishes by hashing and applying, a repo containing symlinks fails before its `graft.toml` entry is written; passing `--symlinks=skip` adds such a repo in one shot rather than requiring a hand-edit and re-run.

**Entry resolution.** Which entry in `graft.toml` `add` operates on is decided by the following rules. These are all manifest-level validations that happen before any network access; a violation fails with exit code 2:

- With `--name` → target the entry whose name exactly matches the `--name` value: if it exists and its `repo` matches the argument (compared in canonical form, see §5.4) → update it; if it exists but points at a **different repo** → error — `add` never re-points an existing entry to another repository; changing the repo, like renaming, is the `graft remove` + same-name `graft add` path; if it does not exist → add it.
- Without `--name` → match existing entries by normalized repo (canonical form, see §5.4):
  - exactly one entry → update that entry, keeping its name (even a custom one).
  - more than one entry (the same repo declared multiple times) → error: list the matching names, suggesting `--name` to disambiguate.
  - no entry → add it with the derived default name; if that name is already taken by an entry pointing at a **different repo** → error, suggesting `--name`. `add` never silently re-points an existing entry to another repo because of a name coincidence.

**The same repo declared multiple times.** The manifest allows the same `repo` in multiple entries — the typical case is taking several subdirectories of a monorepo, each with a different `subdir`. Each entry is resolved and locked independently keyed by `name` (and may be locked at a different version), and they share the same cached bare repository (§5.4). Adding a second entry requires `--name` with an unused name — otherwise, by the rules above, the repo match would land on the first entry and update it instead. Subsequent version updates are done by passing the repo URL again (with `--name` when multiple entries share the repo).

**The ref argument:**

- **Tag** (`@v1.2.0`): the tag name is written as `version` in `graft.toml`; its commit SHA is resolved against the remote and locked in `graft.lock`.
- **Branch or full/partial SHA** (`@main`, `@a3f8c21d`): resolved against the remote to a full commit SHA. Because there is no tag to record, `graft.toml` gets a pseudo-version (`v0.0.0-<timestamp>-<sha12>`, see §3.1) and `graft.lock` locks the full SHA. The install always uses the locked commit, so a later branch move cannot change what is installed.
- **`@latest` or omitted**: graft fetches the remote's tag list and, among all tags matching the SemVer 2.0 format (an optional `v` prefix is allowed, so both `v1.2.0` and `1.2.0` match), picks the highest version. Pre-release tags (e.g. `v2.0.0-rc.1`) are skipped. If no tag matches, it falls back to the remote `HEAD` and writes a pseudo-version.
- **Precedence:** when one ref name is both a tag and a branch, it resolves as a tag — consistent with git's own refname resolution order. A tag literally named `latest` is shadowed by the special meaning of `@latest` (the same trade-off as go.mod); this is a documented limitation.
- Running `graft add repo@main` again later may lock a newer commit — that is exactly the deliberate "update" action, and it shows up as a one-line `version` diff in `graft.toml`.
- A remote that is reachable but the ref does not exist is a general error (exit code 1); a remote that cannot be reached at all is a network error (exit code 3).

**Behavior when the dependency already exists:**

- Resolves to the same commit → no-op, prints `✓ shared-scripts already at a3f8c21 (v1.2.0)`.
- A different commit → update `version` in `graft.toml`, rewrite the matching entry in `graft.lock`, reinstall into vendor.

After rewriting its own entry in `graft.toml`, `add` finishes with full `graft lock` semantics — it re-syncs *every* entry, not just the one it changed, so hand-edits to other dependencies are handled in the same run — and then runs the same reconcile as `graft apply`: the vendor directory is brought to exactly the state of the lockfile — missing dependencies added, surplus removed, mismatched aligned. As a result `add` never fails because of a pre-existing toml ↔ lock mismatch; it resolves that mismatch directly. When this re-sync changes any dependency *other* than the one targeted, `add` prints `also synced other dependencies:` followed by the affected install/remove lines, so a collateral change is never mistaken for part of the requested update.

### 4.3 `lock --check` semantics

**`graft lock --check`**
- Offline-only mode: uses string comparison to verify that `graft.lock` is the up-to-date resolution of `graft.toml`.
- Writes no files and makes no network requests.
- If `graft.lock` does not exist → exits with code 2 and: `graft.lock not found. Run 'graft lock' first.`
- For each manifest entry it compares the matching lockfile entry's `repo`, `version`, `subdir`, and `symlinks`; for each locked entry it confirms the name still appears in the manifest. Any mismatch (addition, removal, or field change) → exits with code 2, listing each out-of-date dependency name and prompting `graft lock` before committing.
- If everything matches → exits with code 0 and prints: `✓ graft.lock is up to date`
- Typical CI usage: run `graft lock --check` to ensure the lockfile is committed in sync with the manifest before proceeding.

### 4.4 `apply` semantics

**`graft apply`**
- Reads `graft.lock` only, ignoring `graft.toml` for version resolution.
- Aligns the vendor directory to the state defined by the lockfile: adds missing dependencies, removes surplus ones, upgrades or downgrades version-mismatched ones.
- If `graft.lock` does not exist → exits with code 2 and: `graft.lock not found. Run 'graft lock' first.`
- If `graft.lock` is out of sync with `graft.toml` (a dependency exists in only one of them, or a dependency's `version`, `repo`, `subdir`, `symlinks`, or resolved `dest` in `graft.toml` differs from what `graft.lock` records) → exits with code 2 and: `graft.toml and graft.lock are out of sync. Run 'graft lock' to update the lockfile.` This check is a pure string comparison — no network.
- If the vendor directory content matches the lockfile hash → skipped (no-op, prints `✓ already up to date`).
- Never modifies `graft.toml` or `graft.lock`.

### 4.5 `status` semantics

**`graft status`**
- Read-only: modifies no files and makes no network access.
- If `graft.lock` does not exist, every dependency in `graft.toml` is reported as `out of sync` (exit code 2).
- For each dependency it reports one of the following states:
  - `ok` — present in toml, lock, and vendor; vendor content matches the locked hash.
  - `missing` — locked but not present in vendor.
  - `modified` — present in vendor but content does not match the locked hash.
  - `out of sync` — toml and lock disagree (the dependency exists on only one side, or `version`/`repo`/`subdir`/`symlinks`/`dest` differ).
  - `extra` — a path under `<dir>` that belongs to no locked dependency (a leftover from a removed dependency, or created by hand). `graft apply` will delete it. A toml ↔ lock disagreement is always reported as `out of sync`, never `extra`. When the lockfile does not exist, `extra` is not reported — everything is already `out of sync`, and an extra report would just be noise.
- A dependency present only in lock and not in toml is likewise reported as `out of sync`.
- Output is an aligned table, one row per `✓/✗ <name>  <short commit> (<version>)  <state>`; rows with no trustworthy lock information (`out of sync` and `extra`) replace the commit column with `-`. For example:

  ```
  ✓ shared-scripts  a3f8c21 (v1.2.0)  ok
  ✗ proto-defs      b7e1209 (v0.8.1)  modified
  ```

  When there are no dependencies to report, prints `✓ no dependencies` and exits with code 0, rather than exiting silently.
- In link mode (§5.4), the vendor check inspects the link target: pointing at `store/<locked hash>` is `ok`, a wrong target is `modified`, a dangling link is `missing`. To verify the integrity of a store entry itself, use `graft cache verify`.
- `status` judges each dest by the current mode: a dest materialized in the other mode — a symlinked dest in copy mode, a real tree in link mode — reports `modified`, which is exactly the drift `apply` would rewrite (§5.4). An all-`ok` status therefore guarantees `apply` is a no-op under the same mode.
- Exits with code 0 when everything is `ok`. A toml↔lock disagreement (`out of sync`) exits with code 2 — the same lockfile-sync failure code as `lock --check` and `apply`; pure vendor drift (`missing`/`modified`/`extra`) exits with code 1. When both occur, the more severe code 2 wins. This lets `graft status` serve as a low-cost CI gate (for example, verifying that a committed `vendor/` has not been hand-edited) without changing anything.

### 4.6 Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error (written to stderr) |
| 2 | Config / lockfile validation error |
| 3 | Network error |
| 4 | Hash mismatch (content integrity failure) |

Distinct exit codes let CI pipelines tell "network outage" apart from "vendor content was tampered with".

`graft status` reuses these codes (see §4.5): an `out of sync` row exits 2 (a lockfile-sync failure, like `lock --check` and `apply`), while pure vendor drift (`missing`/`modified`/`extra`) exits 1.

### 4.7 `graft cache`

The global cache (§5.4) is invisible in normal use; the following subcommands manage it:

- `graft cache dir` — print the cache directory path.
- `graft cache verify` — re-hash every store entry; report and delete corrupted entries (exit code 4 if any are found).
- `graft cache prune` — remove store entries that no registered link-mode dest references *and* that have not been used recently, plus bare repositories not fetched recently, and report the space reclaimed. Keeping recent entries (an age floor) does two things: it avoids racing a concurrent `apply` (an entry inserted but not yet linked is still recent, so it is never reclaimed underfoot), and it gives copy-mode entries a retention window instead of vanishing on the next prune; an expired entry costs only a re-fetch. Safe to run periodically (and in CI). prune never reclaims a store entry that a live link-mode dest still points at (copy-mode vendors don't depend on the store at all), so under normal operation it does not break existing vendors and needs no `apply` to repair; only if the link registry has drifted out of sync with reality could a live link lose its target, showing as `missing` in `graft status` and repaired by `graft apply`. To wipe the cache entirely, use `graft cache clean` instead.
- `graft cache clean` — remove the entire cache (every bare repository and store entry) and report the space reclaimed. The cache is purely a performance layer, so this is always safe (equivalent to manually removing the directory printed by `graft cache dir`, but cross-platform and without hand-running `rm`). clean first checks that the cache root contains the `CACHEDIR.TAG` marker graft created, so a `GRAFT_CACHE_DIR` pointing at the wrong directory can never delete user data. Unlike prune, clean removes the store entries that link-mode vendors point at, leaving their symlinks dangling (`missing` in `graft status`) until `graft apply` re-materializes them (re-fetching as needed); copy-mode vendors are real copies and are unaffected.

### 4.8 Environment variables

| Variable | Default | Description |
|---|---|---|
| `GRAFT_CACHE_DIR` | OS user cache dir (Linux `~/.cache/graft`, macOS `~/Library/Caches/graft`, Windows `%LocalAppData%\graft\cache`) | Override the global cache location. The cache is a pure performance layer and is always safe to delete. |
| `GRAFT_LINK_MODE` | `copy` | Controls the materialization mode (§5.4): `copy` (default) or `symlink`. A per-machine choice; every materializing command (`apply`, `add`, `remove`) honors it identically, and it is never recorded in `graft.toml` or `graft.lock`. |

---

## 5. Architecture

### 5.1 Apply flow

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
  (incremental fetch; fallback in 5.3)       │
     │                                       │
     ▼                                       │
  check <commit> out to <cache>/tmp/,        │
  remove .git (with <subdir> set, sparse     │
  checkout limits the working tree: see 5.3) │
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
  (copy mode: reflink/copy staged under <dir>/.graft-tmp,
   re-verify the hash, then rename;
   link mode: create a symlink / junction)
     │
     ▼
remove items under <dir> matching no locked dest
     │
     ▼
print summary
```

The checkout staging area lives in `<cache>/tmp/`, on the same filesystem as the store, so the rename into `store/` is atomic; if another process is building the same entry concurrently, the loser of the rename race simply uses the existing entry. Copy-mode materialization is staged under `<dir>/.graft-tmp/` rather than the system temp directory — so that the final move into `<dest>` is an atomic same-filesystem rename. A reconcile removes `.graft-tmp` when it finishes — on failure as well as on success; leftover items from an interrupted run in either staging area are also cleaned up on the next state-modifying command, and `.graft-tmp` is never treated as a surplus dependency during reconcile.

**Replacing an existing dest.** When a dep needs reinstalling (a hash mismatch, a mode switch), the currently-installed dest is never deleted up front: it is first renamed aside into `.graft-tmp` (parked), and only removed once the new content has successfully swapped into its place — if the swap-in itself then fails, the parked dest is restored, so a failed reinstall never leaves the dependency partially or fully deleted. If that restore rename itself fails (e.g. dest is still locked), the parked tree is instead moved to `<dest>.graft-backup` next to it, since `.graft-tmp` is otherwise wiped unconditionally once the reconcile returns; the next `apply` treats `<dest>.graft-backup` as a surplus path and removes it (`status` reports it too). If dest sits on a different filesystem than `.graft-tmp` (a custom `dir` mounted separately from the vendor root), there is nothing to park aside, so it is deleted in place instead. Before parking or removing a dest — and likewise before removing the staging directory or a surplus path — graft also checks whether the process's own current working directory is at or inside the path about to be moved or deleted — a directory rename/delete that Windows refuses outright — and fails with a clear error asking the user to `cd` out first (§6) rather than surfacing the underlying OS error; this check applies on every OS, not only Windows, so behavior does not depend on which platform graft runs on. A link-mode dest (a symlink or junction) is exempt: a cwd that resolves through the link refers to the link's target, which replacing or removing the link node never disturbs.

### 5.2 Parallelism

Installs are split into two phases, each with its own worker pool:

1. **Fetch phase** (network-bound): worker count is `min(numDeps, 16)`. Each worker ensures its dep's tree is present in the content store: a store hit returns immediately; otherwise the dep is fetched, hashed, and inserted into the store.
2. **Install phase** (CPU/IO-bound): worker count is `min(numDeps, number of CPU cores)`. Each worker atomically moves the already-prepared store tree into `<dest>` via rename (or copy-on-write reflink).

Both phases collect all errors and report them together after the phase completes (no fail-fast, so you see every error at once). Multiple deps from the same bare repository are still serialized by the per-repo advisory lock (§5.4).

### 5.3 Fetch strategy

All fetches target that dependency's bare cache repository (§5.4) and are incremental — a commit already in the cache is never re-fetched. Fetching directly by commit SHA (`git fetch <repo> <commit>`) only works when the server allows it (`uploadpack.allowReachableSHA1InWant` or `allowAnySHA1InWant`). GitHub, GitLab, and Gitea all enable it; a plain `git daemon` or an older server may not. So graft tries, in order:

1. `git fetch --depth=1 <repo> <commit>` — cheapest; supported by all major hosts.
2. If the server rejects the SHA and the locked entry's `version` is a tag: `git fetch --depth=1 <repo> <tag>`, then check whether `FETCH_HEAD` is exactly `commit`. A mismatch here is not an error — the tag may have been re-pointed — and graft continues to the next step.
3. Fetch all refs in full, then check out `commit` — always correct, but the most bandwidth-expensive.

If the commit cannot be obtained at all, graft distinguishes two causes in the error message: a network failure (exit code 3), or "that commit no longer exists on the remote" (e.g. history was rewritten), suggesting `graft add <name>@<ref>` to re-lock.

For a dependency with `subdir` set, the fetch additionally passes `--filter=blob:none` and configures a sparse checkout of `<subdir>`, so blobs outside the target subdirectory are never downloaded. When the server does not support partial clone, graft silently falls back to a normal fetch — the sparse checkout still limits the working tree to `<subdir>`. Note that filter-excluded blobs are downloaded on demand at checkout, so offline materialization of a `subdir` dependency is only guaranteed once its file tree is in the content store.

**No Git LFS support in v1.** A plain `git` checkout by graft materializes LFS pointer files, and the lockfile hash would silently lock the pointer rather than the real content. If the checked-out tree (after `subdir` filtering) contains a `.gitattributes` declaring an `lfs` filter, the install fails with exit code 2 and an error message naming the dependency — an explicit, documented limitation rather than a silent trap.

### 5.4 Global cache and content store

All downloads flow through a user-level cache (default: the OS user cache directory, e.g. `~/.cache/graft` on Linux, `~/Library/Caches/graft` on macOS, `%LocalAppData%\graft\cache` on Windows; overridable with `GRAFT_CACHE_DIR`):

```
<cache>/
├── CACHEDIR.TAG                    # standard cache-directory marker (bford.info/cachedir; backup tools skip it)
├── repos/<host>/<org>/<repo>.git   # bare repos, incrementally fetched, shared across projects
├── store/sha256/<xx>/<hex…>/       # immutable file-tree snapshots, keyed by lockfile content hash
├── links/                          # registry of link-mode dests (queried by `cache prune`)
├── tmp/                            # checkout staging area (same filesystem as store)
└── locks/                          # advisory locks: per-repo fetch lock + per-project modify lock (§5.5)
```

**Bare-repo cache.** The key is always the canonical `<host>/<org>/<repo>` form (scheme, userinfo, and the `.git` suffix stripped), regardless of how `repo` is written — `https://github.com/org/repo`, `github.com/org/repo`, and `git@github.com:org/repo.git` all share one entry. One advisory file lock per repository serializes concurrent fetches into the same bare repo; the rest of the cache is lock-free via atomic renames. Any commit ever fetched can be reinstalled offline.

**Content store.** A store entry is the complete installed tree for a given lockfile `hash`: checked out to `tmp/`, hashed, verified, then atomically renamed into place with all files made read-only. On disk, entries are sharded by the first two hex digits of the hash — the bucket directory is the first 2 digits and the entry directory the remaining 62 (e.g. hash `4e13…` lives at `store/sha256/4e/13…/`); the entry directory does not repeat the full hash. Because the key *is* the hash recorded in `graft.lock`, a store hit needs no network access. Two benefits fall out naturally: `graft lock` fills the store while it computes the hash, so the following `graft apply` installs with no re-download; and content that is byte-for-byte identical — even from different repos or versions — is stored only once per machine.

**Materialization.** How a store entry becomes `<dest>` is selected by the `GRAFT_LINK_MODE` environment variable. It is a machine-local choice that every materializing command (`apply`, `add`, `remove`) honors identically — there is no per-command flag, and it is never recorded in `graft.toml` or `graft.lock`. For a one-off, set it for a single command (`GRAFT_LINK_MODE=symlink graft apply`). The two mode names (`copy`, `symlink`) mirror uv's link-mode vocabulary; graft deliberately supports only these two:

- **copy** (default) — uses copy-on-write reflink when the filesystem supports it (APFS, btrfs, XFS, ReFS), otherwise a plain copy. Observable behavior is exactly the same as graft without a cache, including the commit-`vendor/` workflow, and `apply` still re-verifies the vendor tree's hash on every run; the tree materialized from the store is also re-hashed before it reaches the vendor directory — a corrupted store entry fails with exit code 4 and is removed (the next run re-fetches it), and is never installed.
- **symlink** (opt-in: `GRAFT_LINK_MODE=symlink`) — `<dest>` becomes a single directory symlink pointing at the store (a junction on Windows, requiring no admin privileges), registered in `links/`. Any number of projects share one on-disk file tree. Verification reduces to a cheap link-target comparison: pointing at `store/<locked hash>` is `ok`, a wrong target is `modified`, a dangling link is `missing`. Limitations: `vendor/` must be gitignored (committing a link is meaningless to other machines), and vendor integrity then rests on the store's immutability — files are read-only, so an accidental edit through the link fails immediately. During reconcile, a dest found materialized in the other mode is treated as drift and rewritten in the current mode.

The cache is purely a performance layer: deleting the entire cache is always safe, and no lockfile guarantee depends on it. GC and inspection are provided by `graft cache` (§4.7).

### 5.5 Concurrency and locking

How mature package managers handle concurrent execution:

- **go** — serializes downloads into the shared module cache with a single advisory lock file (`$GOMODCACHE/cache/lock`); extracted cache entries are created atomically and kept read-only; `go.mod` / `go.sum` are written through a locked file.
- **Cargo** — an advisory lock on the global package cache (`$CARGO_HOME/.package-cache`) plus a per-project build-directory lock; a second cargo process blocks and prints `Blocking waiting for file lock on package cache` until the first finishes.
- **uv** — advisory file locks scoped to the resource being modified (a cache shard, a target environment); concurrent commands wait rather than error.

The common pattern: protect the global cache with an advisory file lock plus atomic, immutable entries; serialize project-level modifications with one lock per project; make waiters block with a message rather than error. graft follows the same pattern:

- **Cache side** (§5.4): a per-repo fetch lock; store entries are immutable and created by atomic rename — no whole-cache global lock needed.
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

# cwd inside the dest being replaced
error: cannot replace "shared-scripts": current directory is inside it

  cd out of it first, then re-run
```

---

## 7. Security considerations

**Ref mutability.** Branches move and tags can be force-pushed, so what graft installs is never resolved from a ref at install time: `graft apply` reads only `commit` from the lockfile. The manifest's `version` is resolved against the remote exactly once — only when a dependency is new, or when its `repo`/`version` changes. An unchanged `(repo, version)` pair keeps the locked commit, so a re-pointed tag cannot silently change what is installed; an untagged lock uses a pseudo-version with the commit SHA embedded, requiring no ref lookup at all. The only remaining trust point is the first resolution (and a re-resolution after deleting the lockfile) — the same as Cargo, and equivalent to the go.mod trust model without a sumdb. The content hash in `graft.lock` additionally guards the installed file tree itself: a hand-edited `vendor/` or a tampered lockfile makes `graft apply` fail with exit code 4 (shown as `modified` in `graft status`).

**No arbitrary code execution.** Dependencies are static file trees; graft never executes anything within them.

**Path safety.** `dir` must be a relative path inside the repository; `name` names a path under `<dir>` (and `subdir` selects a subdirectory of the remote repo) — absolute paths, `..` segments, and any `.git` segment (which would let the destructive vendor reconcile overlap the git repository) are rejected at the validation stage (exit code 2); a `name` whose first segment starts with the reserved `.graft-` prefix (matched case-insensitively, so it also catches case variants that collide on case-insensitive filesystems) is likewise rejected (exit code 2), since that prefix is reserved for the reconcile staging directory and other internal directories, and such an install path would overlap one of them and could never apply; the fully-resolved install path (`<dir>/<name>`) always lands inside the install tree, so a malicious or corrupt manifest/lockfile can never direct an install, or a reconcile delete, outside it. Within the fetched file tree, git itself refuses to track paths containing `..` or `.git`, so a malicious dependency cannot escape its own install root either.

**Write-back safety (TOML safety).** `graft add` and `graft remove` rewrite `graft.toml` in place (§3.1), emitting field values verbatim inside a TOML basic string with no escaping; a double quote (`"`), backslash (`\`), or control character in `repo`, `version`, `subdir`, or the top-level `dir` is therefore rejected by `graft add` and at load with exit code 2 — git itself allows some of these characters in tag names, so refs are not implicitly safe — guaranteeing that a manifest graft writes can always be parsed again (`name` is already covered by the §3.1 character whitelist).

**Shared cache.** The cache (§5.4) is user-level and in the same trust domain as the projects that use it. Every store entry is hash-verified at creation and kept read-only; `graft cache verify` can re-check all entries at any time. In copy mode, `apply` re-verifies the vendor tree on every run and re-hashes the tree it materializes from the store before it reaches the vendor directory, exactly as without a cache — a corrupted entry fails with exit code 4 and is removed, never reaching vendor.

**HTTPS by default.** A scheme-less repository path (`github.com/org/repo`) is fetched over HTTPS; explicit `https://` or SSH URLs (`git@github.com:org/repo.git`) are also accepted. No supported spelling needs whitespace, so a `repo` containing any whitespace (space, tab, newline, or other Unicode whitespace) is rejected by `graft add` and at load with exit code 2 — before any network access — so an obviously-malformed value is not reported as "malformed input" by git and misclassified as an exit-3 network error. A local `file://` path with a literal space still works: percent-encode the space as `%20`. Because graft invokes external `git`, all git credential mechanisms — credential helpers, `~/.netrc`, SSH agent, and user-level `url.<base>.insteadOf` rewrites (for example, globally forcing SSH) — apply automatically with no extra configuration. graft runs every `git` invocation with the environment variable `GIT_TERMINAL_PROMPT=0` and the command-line override `-c credential.interactive=false` — a command-line override always wins over the environment, so it can never collide with or shadow a `GIT_CONFIG_*` credential rewrite (e.g. `url.<base>.insteadOf`) the caller's own environment already sets — so a private HTTPS repo with no cached credentials fails fast instead of hanging on a credential helper's own interactive or GUI prompt. This does not reach ssh's own prompts (host-key confirmation, an encrypted key's passphrase) on an SSH remote, which can still block.

---

## 8. Testing strategy

**Unit tests** cover all pure functions: config parsing, lockfile parsing, hash computation, version-string parsing.

**Integration tests** use local bare git repositories as fixture remotes, created on the fly under a temp directory. No network access is needed. Each test creates a temporary working directory, runs graft commands as subprocesses, and verifies file content and exit codes.

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
- `subdir` subdirectory support (sparse checkout)

### Milestone 3 — polish
- parallel installs (worker pool)
- state-modifying commands take a per-project advisory lock (§5.5)
- clear error messages on all error paths
- install script (`curl | sh`)

### Milestone 4 — caching and deduplication
- global bare-repo cache + content-addressed store (copy mode, reflink where supported)
- `graft cache dir` / `verify` / `prune` / `clean`
- opt-in link mode (symlink / junction dest, `links/` registry)

### Milestone 5 — ecosystem
- GitHub Actions example
- GitLab CI example
- documentation site
