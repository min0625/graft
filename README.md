**English** | [繁體中文](https://github.com/min0625/graft/blob/main/README.zh-TW.md)

# graft

> Graft external git repositories into your project — like npm, but for any repo.

`graft` is a language-agnostic dependency manager for git repositories. It lets you declare, version-lock, and install dependencies from other git repos — whether they contain shell scripts, proto definitions, CI templates, or anything else — with a familiar package manager experience.

```bash
$ graft add github.com/your-org/shared-scripts@v1.2.0
✓ shared-scripts a3f8c21 (v1.2.0)

# after a fresh clone, or in CI:
$ graft apply
✓ shared-scripts a3f8c21 (v1.2.0)
```

---

## Why graft?

| | git submodule | git subtree | Gitman | vdm | **graft** |
|---|---|---|---|---|---|
| Intuitive CLI | ✗ | ✗ | ✓ | ✓ | ✓ |
| Lockfile | partial | ✗ | ✓ | ✗ | ✓ |
| Single binary | ✓ | ✓ | ✗ (pip) | ✓ | ✓ |
| Keeps foreign history out | ✓ | ✗ | ✓ | ✓ | ✓ |
| Parallel install | ✗ | ✗ | ✗ | ✗ | ✓ |
| Content hash verification | ✗ | ✗ | ✗ | ✗ | ✓ |

---

## Requirements

- `git` on `$PATH`
- macOS, Linux, or Windows

## Installation

**Go**

```bash
go install github.com/min0625/graft@latest
```

---

## Quick start

```bash
# 1. Initialize graft in your repo — the argument names the install directory
graft init vendor

# 2. Add a dependency — this resolves, locks, and installs it in one step
graft add github.com/your-org/shared-scripts@v1.2.0

# 3. After a fresh clone (or in CI), reinstall everything from the lockfile
graft apply
```

This creates two files:

- `graft.toml` — your dependency manifest (commit this)
- `graft.lock` — the lockfile with pinned SHAs and content hashes (commit this)

---

## Commands

### `graft init <vendor>`

Initialize graft in the current directory. The required argument names the root directory dependencies are installed into — there is no default, so the choice is always explicit. Creates `graft.toml`; fails if it already exists — it never overwrites.

```bash
graft init vendor    # or: graft init deps, graft init third_party, …
```

> Tip: in Go or PHP projects `vendor/` already belongs to the toolchain (`go mod vendor`, Composer) — pick another name like `deps`.

---

### `graft add <repo>[@ref]`

Add a new dependency, or update an existing one. Updates `graft.toml`, regenerates `graft.lock`, and reconciles the vendor directory.

```bash
graft add github.com/your-org/shared-scripts@v1.2.0    # pin a tag's commit
graft add github.com/your-org/shared-scripts@main       # pin a branch's current commit
graft add github.com/your-org/shared-scripts@a3f8c21d   # pin a SHA
graft add github.com/your-org/shared-scripts             # pin the latest tag's commit
graft add shared-scripts@v1.3.0                          # update an existing dep by name
```

Whatever ref you pass — tag, branch, SHA, or nothing (resolves the latest semver tag) — graft resolves it on the remote and records it go.mod-style: `graft.toml` gets a human-readable `version` (the tag, or a pseudo-version like `v0.0.0-20260418091327-a3f8c21d4e8f` when there is no tag), while the exact commit SHA and a content hash go into `graft.lock`. Installs always use the locked commit, so a branch moving or a tag being re-pointed later can never change what gets installed. To pick up new commits, run `graft add` again.

For a dependency that already exists in `graft.toml`, you can pass its name instead of the full repo URL. If the dep is already pinned to the same commit, the command is a no-op.

`graft add` finishes by re-syncing the *entire* lockfile and vendor tree (the same as `graft lock` + `graft apply`), so hand-edits you've made to other deps in `graft.toml` are picked up in the same run.

Options:

```
--dest <dir>       Where to place the dependency locally (default: <vendor>/<name>)
--path <dir>       Subdirectory of the remote repo to install (default: repo root)
--name <name>      Dependency name (default: the repo's last path segment)
```

Example with options:

```bash
graft add github.com/your-org/devtools@v2.0.0 \
  --dest tools/shared \
  --name devtools-scripts
```

---

### `graft apply`

Reconcile the vendor directory to exactly match `graft.lock`: add missing deps, remove extra deps, upgrade or downgrade version-mismatched deps. Never modifies the lockfile.

This is the command to use in CI.

```bash
graft apply
```

If `graft.lock` is missing or out of sync with `graft.toml`, graft will exit with a non-zero code and tell you what to run.

---

### `graft lock`

Re-sync `graft.lock` from `graft.toml` without installing anything.

```bash
graft lock
```

Useful when you've manually edited `graft.toml` (e.g. bumped a `version` to a newer tag) and want to update the lockfile before running `graft apply`. Entries whose `repo` and `version` are unchanged keep their locked commit — no network access for them. New entries and entries whose `repo` or `version` changed are re-resolved and downloaded (to compute the lockfile's content hash); changing only `path` re-downloads the locked commit to recompute the hash, without re-resolving the version. Nothing is installed into vendor.

---

### `graft remove <name>`

Remove a dependency from `graft.toml` and `graft.lock`, and delete its local files.

```bash
graft remove shared-scripts
```

---

### `graft status`

Show the sync state of `graft.toml`, `graft.lock`, and the vendor directory. Read-only — modifies no files and makes no network requests.

```bash
$ graft status
✓ shared-scripts  a3f8c21 (v1.2.0)  ok
✗ proto-defs      b7e1209 (v0.8.1)  modified (vendor content differs from lockfile)
```

Exits 0 when everything is in sync, 1 otherwise — handy as a CI guard against hand-edited vendored files.

---

## Configuration

`graft.toml` is the manifest file. Commit it to your repository.

```toml
vendor = "vendor"   # where dependencies are stored (required; set by `graft init <vendor>`)

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
path    = "proto"          # optional: install only this subdirectory of the repo
dest    = "vendor/proto"   # optional: custom install location (default: <vendor>/<name>)
```

Notes:

- `repo` may omit the scheme — scheme-less paths like `github.com/org/repo` are fetched over HTTPS, go.mod-style. Write `git@github.com:org/repo.git` explicitly for SSH.
- `version` is go.mod-style: a git tag when one exists, otherwise a pseudo-version (`v0.0.0-<timestamp>-<sha12>`) that embeds the commit. You can hand-edit it to a newer tag and run `graft lock`.
- The resolved commit SHA and content hash live only in `graft.lock`, and installs only ever use those — so a moving branch or a re-pointed tag can't silently change your dependencies.
- Commands can be run from any subdirectory: graft walks up from the current directory to the nearest `graft.toml` (never past the git repository root) and treats that directory as the project root.
- Git LFS is not supported: if a dependency's tree uses LFS (`filter=lfs` in its `.gitattributes`), graft fails with a clear error instead of silently vendoring pointer files.

### `graft.lock`

The lockfile is auto-generated by graft. Commit it to your repository. Do not edit manually.

```toml
# This file is auto-generated by graft. Do not edit manually.
# Run `graft lock` to regenerate.

lock_version = 1

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"
dest    = "vendor/shared-scripts"
commit  = "a3f8c21d4e8f1b2c3d4e5f6a7b8c9d0e1f2a3b4c"
time    = 2026-04-18T09:13:27Z
hash    = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
path    = "proto"
dest    = "vendor/proto"
commit  = "b7e1209fa3c8d2e1f0a9b8c7d6e5f4a3b2c1d0e9"
time    = 2026-02-02T18:40:11Z
hash    = "sha256:a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
```

`time` is the committer timestamp of the pinned commit (UTC) — informational only, handy for seeing at a glance how old a dependency is.

---

## CI usage

**GitHub Actions**

```yaml
steps:
  - uses: actions/checkout@v4

  - uses: actions/setup-go@v5
    with:
      go-version: stable

  - name: Install graft
    run: go install github.com/min0625/graft@latest

  - name: Cache graft downloads
    uses: actions/cache@v4
    with:
      path: ~/.cache/graft
      key: graft-${{ hashFiles('graft.lock') }}

  - name: Apply dependencies
    run: graft apply
```

**GitLab CI**

```yaml
before_script:
  - go install github.com/min0625/graft@latest
  - graft apply
```

---

## .gitignore

Add the vendor directory to `.gitignore`:

```
vendor/
```

Or skip the `.gitignore` entry and commit the vendored dependencies — useful for reproducible builds without network access. Both workflows are supported: `graft apply` with a committed `vendor/` directory is a no-op when the contents match the lockfile, and `graft status` catches hand-edits to it in CI.

---

## Caching & deduplication

graft keeps a per-user global cache (location: `graft cache dir`; override with `GRAFT_CACHE_DIR`):

- **Bare repo cache** — fetches are incremental, so a commit that was ever downloaded is never downloaded again, and re-installs work offline.
- **Content store** — every installed tree is stored once, keyed by its lockfile content hash. `graft lock` followed by `graft apply` downloads each dep only once, and identical content shared by several projects is fetched and stored once per machine.

By default vendor directories are real copies (using copy-on-write reflinks when the filesystem supports them). With `graft apply --link` (or `GRAFT_LINK_MODE=symlink`), each dest instead becomes a directory symlink — a junction on Windows, no admin rights needed — into the store, so any number of projects share a single on-disk copy. Link mode requires a gitignored `vendor/` and is a per-machine choice; it is never recorded in `graft.toml` or `graft.lock`.

```bash
graft cache dir      # print the cache location
graft cache verify   # re-hash store entries, drop corrupted ones
graft cache clean    # remove unreferenced entries (--all: delete everything)
```

The cache is purely a performance layer — deleting it is always safe.

---

## Concurrent runs

Mutating commands (`add`, `remove`, `apply`, `lock`) take a per-project advisory lock, so a second graft process — say, two CI jobs sharing a workspace — waits for the first to finish instead of corrupting the vendor directory. This is the same behavior as cargo or uv. The lock file lives in the global cache, never in your repository. `graft status` is read-only and never blocks.

---

## Comparison with alternatives

**vs git submodule**
Submodules require extra commands after every clone (`git submodule update --init --recursive`), have confusing state management, and lack a proper lockfile. graft is a single command: `graft apply`.

**vs git subtree**
Subtree merges the dependency's entire commit history into your repository and has no manifest — there is no single file that says what you depend on and at which version. graft keeps foreign history out and records every dependency in `graft.toml` / `graft.lock`.

**vs Gitman**
Gitman requires Python 3.10+. graft is a single binary with no installation dependencies. Both support lockfiles, but graft adds content hash verification and parallel installs. Like Gitman, graft does not recursively resolve transitive dependencies — you explicitly declare all dependencies you need. This keeps the tool simple and transparent.

**vs vdm**
vdm has no lockfile — if you pin to a branch, you get different code on different days. graft always records the exact commit SHA and a content hash. Like vdm, graft only manages top-level dependencies you explicitly declare.

---

## License

Apache License 2.0 — see [LICENSE](https://github.com/min0625/graft/blob/main/LICENSE) for details.
