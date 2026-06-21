**English** | [繁體中文](https://github.com/min0625/graft/blob/main/README.zh-TW.md)

# graft

> Graft external git repositories into your project — like npm, but for any repo.

`graft` is a language-agnostic dependency manager for git repositories. It lets you declare, version-lock, and install dependencies from other git repos — whether they contain shell scripts, proto definitions, CI templates, or anything else — with a familiar package manager experience.

```bash
$ graft add github.com/your-org/shared-scripts@v1.2.0
✓ added shared-scripts v1.2.0 (a3f8c21)

# after a fresh clone, or in CI:
$ graft apply
✓ installed shared-scripts v1.2.0 (a3f8c21)
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

### Automated install

**macOS / Linux**

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/min0625/graft/main/script/install.sh)"
```

Auto-detects OS and architecture (Linux/macOS, x86_64/arm64), installs to `~/.local/bin`. Override with `GRAFT_INSTALL_DIR` or pin a version with `GRAFT_VERSION=v1.0.0`.

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/min0625/graft/main/script/install.ps1 | iex
```

Installs to `$HOME\.local\bin`. Override with `$env:GRAFT_INSTALL_DIR` or pin a version with `$env:GRAFT_VERSION = 'v1.0.0'`.

### Manual download

Pre-built binaries for Linux, macOS, and Windows (amd64/arm64) are available at [GitHub Releases](https://github.com/min0625/graft/releases).

### Shell completion

Run the appropriate command once and source it from your shell profile:

```bash
# Bash (~/.bashrc or ~/.bash_profile)
source <(graft completion bash)

# Zsh (~/.zshrc — also requires compinit)
source <(graft completion zsh)

# Fish (~/.config/fish/config.fish)
graft completion fish | source

# PowerShell ($PROFILE)
graft completion powershell | Out-String | Invoke-Expression
```

Or persist the script for faster startup:

```bash
# Bash
graft completion bash > /etc/bash_completion.d/graft

# Zsh
graft completion zsh > "${fpath[1]}/_graft"
```

---

## Quick start

```bash
# 1. Initialize graft in your repo — the argument names the install directory
graft init deps

# 2. Add a dependency — this resolves, locks, and installs it in one step
graft add github.com/your-org/shared-scripts@v1.2.0

# 3. After a fresh clone (or in CI), reinstall everything from the lockfile
graft apply
```

This creates two files:

- `graft.toml` — your dependency manifest (commit this)
- `graft.lock` — the lockfile with pinned SHAs and content hashes (commit this)

…and installs each dependency at `<dir>/<name>`:

```
your-project/
├── graft.toml
├── graft.lock
└── deps/                  # the install dir you named in `graft init`
    ├── shared-scripts/    # the whole repo root
    └── proto-defs/        # only the subtree named by `subdir`, if set
```

---

## Commands

### `graft init [dir]`

Initialize graft in the current directory. The optional argument sets the root directory dependencies are installed into; defaults to `deps`. Creates `graft.toml`; fails if it already exists — it never overwrites.

```bash
graft init              # creates dir = "deps"
graft init third_party  # explicit name
```

> Tip: in Go or PHP projects `vendor/` already belongs to the toolchain (`go mod vendor`, Composer) — `deps` (the default) avoids that conflict.

---

### `graft add <repo>[@ref]`

Add a new dependency, or update an existing one. Updates `graft.toml`, regenerates `graft.lock`, and reconciles the vendor directory.

```bash
graft add github.com/your-org/shared-scripts@v1.2.0    # pin a tag's commit
graft add github.com/your-org/shared-scripts@main       # pin a branch's current commit
graft add github.com/your-org/shared-scripts@a3f8c21d   # pin a SHA
graft add github.com/your-org/shared-scripts             # pin the latest tag's commit
graft add github.com/your-org/shared-scripts@v1.3.0     # update an existing dep by repo URL
```

How the ref you pass is recorded as a `version` in `graft.toml`:

| You pass | `version` becomes |
|---|---|
| a tag (`@v1.2.0`) | the tag name, verbatim |
| a branch or SHA (`@main`, `@a3f8c21d`) | a pseudo-version like `v0.0.0-20260418091327-a3f8c21d4e8f` |
| nothing | the latest semver tag (or a pseudo-version of the remote `HEAD` if there are none) |

In every case the exact commit SHA and content hash go into `graft.lock`, and installs **only ever use those** — a branch moving or a tag being re-pointed later can never change what gets installed.

If the dep is already pinned to the same commit, the command is a no-op. `graft add` finishes by re-syncing the *entire* lockfile and vendor tree (like `graft lock` + `graft apply`), so hand-edits you've made to other deps in `graft.toml` are picked up in the same run. When that re-sync changes any dependency other than the one you named, `graft add` prints an `also synced other dependencies:` block listing them, so a collateral change never hides behind the line for the dep you asked for:

```bash
$ graft add github.com/your-org/shared-scripts@v1.3.0
✓ updated shared-scripts to v1.3.0 (c4d9e02)
also synced other dependencies:
✓ installed proto-defs v0.9.0 (f1a2b3c)   # a version you'd hand-edited in graft.toml
```

Options:

```
--name <name>      Dep name and install path under vendor (e.g. tools or tool-a/util)
--subdir <dir>     Subdirectory of the remote repo to install (default: repo root)
--symlinks <mode>  Symlink policy: reject (default) or skip (sets symlinks in graft.toml)
```

The `--name` value becomes the dep's name and install path: `--name tools` installs at `<dir>/tools`, `--name tool-a/util` installs at `<dir>/tool-a/util`. To rename a dep, `graft remove` it and `graft add` with the new `--name`.

The same repo can appear more than once — e.g. two subdirectories of a monorepo — as long as each entry gets its own `--name`.

```bash
graft add github.com/your-org/monorepo@v1.4.0 --subdir packages/proto --name monorepo-proto
graft add github.com/your-org/monorepo@v1.4.0 --subdir packages/scripts --name monorepo-scripts
graft add github.com/your-org/monorepo@v1.5.0 --name monorepo-proto    # update one entry
```

Without `--name`, graft targets an existing entry by repo. If the repo matches more than one entry, or the default name derived from the repo is already taken by a *different* repo, `graft add` fails with a hint instead of silently re-pointing anything.

---

### Updating a dependency

There is no separate `update` command — `graft add` does both. To update an existing dependency:

```bash
graft add github.com/your-org/shared-scripts@v1.3.0   # to a newer tag
graft add github.com/your-org/shared-scripts@main      # re-resolve a pinned branch to its newest commit
graft add github.com/your-org/shared-scripts           # to the latest semver tag
```

Each update appears as a one-line `version` change in `graft.toml`. For a tag bump you can also hand-edit `version` in `graft.toml` and run `graft lock`; pseudo-versions can't be hand-calculated, so re-run `graft add` for those. When several entries share one repo, add `--name` to pick which one to update.

---

### `graft apply`

Reconcile the vendor directory to exactly match `graft.lock`: add missing deps, remove extra deps, upgrade or downgrade version-mismatched deps. Never modifies the lockfile.

This is the command to use in CI.

```bash
graft apply
```

If `graft.lock` is missing or out of sync with `graft.toml`, graft will exit with a non-zero code and tell you what to run.

With `GRAFT_LINK_MODE=symlink`, dests become directory symlinks into the shared content store instead of copies — see [Caching & deduplication](#caching--deduplication).

---

### `graft lock`

Re-sync `graft.lock` from `graft.toml` without installing anything.

```bash
graft lock
```

Useful when you've manually edited `graft.toml` (e.g. bumped a `version` to a newer tag) and want to update the lockfile before running `graft apply`. Entries whose `repo` and `version` are unchanged keep their locked commit — no network access for them. New entries and entries whose `repo` or `version` changed are re-resolved and downloaded (to compute the lockfile's content hash); changing only `subdir` or `symlinks` re-downloads the locked commit to recompute the hash, without re-resolving the version. Nothing is installed into vendor.

#### `--check` — CI gate

```bash
graft lock --check
```

Verify that `graft.lock` is already the up-to-date resolution of `graft.toml` **without writing any files**. Exits 0 if everything is in sync; exits 2 and lists the out-of-date dependency names if not. Use this in CI to catch "forgot to run `graft lock` before committing".

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
✗ proto-defs      b7e1209 (v0.8.1)  modified
```

The state in the last column is one of:

| State | Meaning |
|---|---|
| `ok` | installed and matches the lockfile |
| `missing` | in the lockfile, absent from the vendor directory |
| `modified` | vendored content differs from the locked hash (e.g. a hand-edited file) |
| `extra` | in the vendor directory, absent from the lockfile |
| `out of sync` | `graft.toml` and `graft.lock` disagree (run `graft lock`) |

Exits 0 when everything is in sync, 1 on vendor-directory drift (missing/modified/extra), and 2 when `graft.toml` and `graft.lock` disagree (the same lockfile-out-of-sync code as `graft lock --check` and `graft apply`; the higher code wins when both occur) — handy as a CI guard against hand-edited vendored files. With no dependencies it prints `✓ no dependencies`. For link-mode dests the check is a cheap link-target comparison (the store is immutable; use `graft cache verify` to re-hash store entries).

---

### `graft cache`

Inspect and manage the per-user global cache. These commands never touch project files and need no `graft.toml`. See [Caching & deduplication](#caching--deduplication) for details.

```bash
graft cache dir      # print the cache location
graft cache verify   # re-hash store entries, drop corrupted ones (exit 4 if any)
graft cache prune    # remove unused entries and stale repos (safe to run periodically)
graft cache clean    # remove the entire cache
```

---

## Configuration

`graft.toml` is the manifest file. Commit it to your repository.

```toml
dir = "deps"        # where dependencies are stored (set by `graft init`)

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
subdir   = "proto"   # optional: install only this subdirectory of the repo
symlinks = "skip"    # optional: "reject" (default) or "skip" symlinks instead of failing (exit code 2)
```

Notes:

- `repo` may omit the scheme — scheme-less paths like `github.com/org/repo` are fetched over HTTPS, go.mod-style. Explicit `https://` or SSH URLs (`git@github.com:org/repo.git`) are also accepted. Because graft invokes external `git`, all git credential mechanisms — credential helpers, `~/.netrc`, SSH agent, and `url.insteadOf` rewrites — apply automatically.
- `version` is go.mod-style: a git tag when one exists, otherwise a pseudo-version (`v0.0.0-<timestamp>-<sha12>`) that embeds the commit. For tags, you can hand-edit it to a newer tag and run `graft lock`. Pseudo-versions are derived and cannot be hand-calculated — re-run `graft add` to change them.
- The resolved commit SHA and content hash live only in `graft.lock`, and installs only ever use those — so a moving branch or a re-pointed tag can't silently change your dependencies.
- Commands can be run from any subdirectory: graft walks up from the current directory to the nearest `graft.toml` (never past the git repository root) and treats that directory as the project root.
- Git LFS is not supported: if a dependency's tree uses LFS (`filter=lfs` in its `.gitattributes`), graft fails with a clear error instead of silently vendoring pointer files.
- Symlinks are rejected by default (exit code 2, naming the specific symlink). If an upstream repo contains incidental symlinks you don't need (doc links, compat aliases), set `symlinks = "skip"` on that dependency — graft skips all symlinks and prints a warning per skipped file; the vendor directory remains symlink-free. To add such a repo in one shot, pass `graft add --symlinks=skip` (it writes the key for you).

### `graft.lock`

The lockfile is auto-generated by graft. Commit it to your repository. Do not edit manually.

```toml
# This file is auto-generated by graft. Do not edit manually.
# Run `graft lock` to regenerate.

lock_version = 1
dir = "deps"

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"
commit  = "a3f8c21d4e8f1b2c3d4e5f6a7b8c9d0e1f2a3b4c"
time    = 2026-04-18T09:13:27Z
hash    = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
subdir  = "proto"
commit  = "b7e1209fa3c8d2e1f0a9b8c7d6e5f4a3b2c1d0e9"
time    = 2026-02-02T18:40:11Z
hash    = "sha256:a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
```

`time` is the committer timestamp of the pinned commit (UTC) — informational only, handy for seeing at a glance how old a dependency is.

---

## Private repositories

graft shells out to your system `git`, so it authenticates exactly the way `git clone` already does on your machine — there are no graft-specific tokens or config. If `git clone <repo>` works, `graft add <repo>` works.

- **SSH:** use an SSH URL and your existing agent/keys: `graft add git@github.com:your-org/private-repo.git@v1.2.0`.
- **HTTPS:** a configured credential helper, a `~/.netrc`, or `url.insteadOf` rewrites all apply. For example, to make every scheme-less `github.com/...` dep use SSH:

  ```bash
  git config --global url."git@github.com:".insteadOf "https://github.com/"
  ```

- **CI:** provide credentials the same way you would for `git clone` — a deploy key / SSH agent, or a token in the checkout step (e.g. GitHub Actions' `actions/checkout` token, or `git config --global url."https://x-access-token:${TOKEN}@github.com/".insteadOf "https://github.com/"`).

---

## CI usage

**GitHub Actions**

```yaml
steps:
  - uses: actions/checkout@v4

  - name: Install graft
    run: /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/min0625/graft/main/script/install.sh)"

  - name: Cache graft downloads
    uses: actions/cache@v4
    with:
      path: ~/.cache/graft
      key: graft-${{ hashFiles('graft.lock') }}

  - name: Check lockfile is up to date
    run: graft lock --check

  - name: Apply dependencies
    run: graft apply
```

**GitLab CI**

```yaml
before_script:
  - /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/min0625/graft/main/script/install.sh)"
  - graft lock --check
  - graft apply
```

---

## .gitignore

Add the install directory (the `dir` you set in `graft init` — `deps` by default) to `.gitignore`:

```
deps/
```

Or skip the `.gitignore` entry and commit the vendored dependencies — useful for reproducible builds without network access. Both workflows are supported: `graft apply` with a committed install directory is a no-op when the contents match the lockfile, and `graft status` catches hand-edits to it in CI.

---

## Caching & deduplication

graft keeps a per-user global cache (location: `graft cache dir`; override with `GRAFT_CACHE_DIR`):

- **Bare repo cache** — fetches are incremental, so a commit that was ever downloaded is never downloaded again, and re-installs work offline.
- **Content store** — every installed tree is stored once, keyed by its lockfile content hash. `graft lock` followed by `graft apply` downloads each dep only once, and identical content shared by several projects is fetched and stored once per machine.

By default vendor directories are real copies (using copy-on-write reflinks when the filesystem supports them). The `GRAFT_LINK_MODE` environment variable selects how dests are materialized — the mode names (`copy`, `symlink`) mirror uv's — and every materializing command (`apply`, `add`, `remove`) honors it identically. With `GRAFT_LINK_MODE=symlink`, each dest instead becomes a directory symlink — a junction on Windows, no admin rights needed — into the store, so any number of projects share a single on-disk copy. Symlink mode requires a gitignored install directory and is a per-machine choice; it is never recorded in `graft.toml` or `graft.lock`. For a one-off, set it for a single command (`GRAFT_LINK_MODE=symlink graft apply`).

```bash
graft cache dir      # print the cache location
graft cache verify   # re-hash store entries, drop corrupted ones
graft cache prune    # remove unused entries and stale repos (safe to run periodically)
graft cache clean    # remove the entire cache
```

The cache is purely a performance layer — deleting it is always safe.

---

## Concurrent runs

Mutating commands (`add`, `remove`, `apply`, `lock`) take a per-project advisory lock, so a second graft process — say, two CI jobs sharing a workspace — waits for the first to finish instead of corrupting the vendor directory. This is the same behavior as cargo or uv. The lock file lives in the global cache, never in your repository. `graft status` is read-only and never blocks.

---

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `GRAFT_CACHE_DIR` | OS user cache dir (e.g. `~/.cache/graft`) | Override the global cache location. Safe to delete at any time; graft re-fetches as needed. |
| `GRAFT_LINK_MODE` | `copy` | How deps are materialized into vendor: `copy` (default, reflink when supported) or `symlink` (directory symlink / Windows junction into the content store). A per-machine choice; never recorded in `graft.toml` or `graft.lock`. |

Both variables are honored by every command that uses the respective feature. To apply a one-off override: `GRAFT_LINK_MODE=symlink graft apply`.

---

## FAQ

**How do I update a dependency?** Run `graft add` again — see [Updating a dependency](#updating-a-dependency). There is no separate `update` command.

**Does graft resolve transitive dependencies?** No. graft only manages the top-level dependencies you explicitly declare — a dependency's own `graft.toml` (if any) is ignored. This keeps resolution simple and transparent; declare everything you need.

**What if upstream deletes or re-points a tag?** Already-installed deps are unaffected — `graft apply` installs from the commit SHA in `graft.lock`, not the tag, so it keeps working even if the tag moves or disappears. You only hit the remote when you re-run `graft add`/`graft lock` for that dep.

**Can I vendor several subdirectories of one monorepo?** Yes — add the repo multiple times, each with its own `--name` and `--subdir`. See [`graft add`](#graft-addreporef).

**How do I list my dependencies / check they're intact?** `graft status` prints every dep with its pinned commit and sync state (`ok` / `missing` / `modified` / `extra` / `out of sync`), read-only and offline. Use it as a CI guard that the install directory wasn't hand-edited.

**Should I commit the install directory?** Either way works. `.gitignore` it for the usual package-manager flow (`graft apply` re-creates it), or commit it for offline/reproducible builds — see [.gitignore](#gitignore).

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

## Design

The full design and behavioral specification lives in [`docs/design.zh-TW.md`](docs/design.zh-TW.md) (authoritative) and [`docs/design.md`](https://github.com/min0625/graft/blob/main/docs/design.md) (English translation) — file formats, command semantics, exit codes, architecture, security considerations, and the testing strategy.

---

## License

Apache License 2.0 — see [LICENSE](https://github.com/min0625/graft/blob/main/LICENSE) for details.
