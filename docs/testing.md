# Graft CLI Black-Box Testing Manual

This document provides a set of reproducible end-to-end (black-box) verification procedures. It allows contributors or AI agents to verify whether the `graft` binary's behavior complies with specifications (`docs/design.zh-TW.md` §4 and `REQ-*` in `docs/requirements.md`) without reading the source code.

> This manual verifies the "actual behavior of the compiled binary." It complements `make test` (unit/golden tests): unit tests cover program logic, while this manual covers "user experience and exit codes during actual command execution."

## 1. Prerequisites

```bash
# 1) Compile the binary under test
make build                      # Produces ./bin/graft

# 2) Isolate global cache to avoid polluting the actual environment (cache is a performance layer; deletion is always safe)
export GRAFT=$PWD/bin/graft
export GRAFT_CACHE_DIR=$(mktemp -d)/graft-cache

# 3) Verify version and network
$GRAFT --version
git ls-remote https://github.com/uber-go/goleak HEAD   # Must have external network access
```

**Test Repositories** (covering different characteristics):

| Repo | Characteristic | Purpose |
|------|----------------|---------|
| `github.com/uber-go/goleak` | Has SemVer tags (latest v1.3.0, annotated) | tag / @latest / SHA |
| `github.com/uber-go/nilaway` | No tags, contains symlinks (`AGENTS.md`, `CLAUDE.md`) | pseudo-version / symlink rejection and skip |
| `github.com/min0625/mint` | Multiple tags (pre-release and stable; exact versions evolve upstream) | --subdir / multi-tag |
| `github.com/min0625/graft` | graft itself | dogfood |

## 2. Conventions

- Use an independent temporary project directory for each scenario: `cd $(mktemp -d)` to avoid interference.
- **Do not pipe exit codes to `head`** (closing a pipe early returns 141 due to SIGPIPE, not the actual exit code):
  ```bash
  $GRAFT <cmd>; echo "exit=$?"
  ```
- Expected exit code reference (design §4.6): `0` Success / `1` General error or status drift / `2` Configuration or lockfile validation error / `3` Network error / `4` Content hash (integrity) failure.

## 3. Test Matrix

Each row: Command → Expected Result (including exit code) → Corresponding REQ. Execute and verify item by item.

### 3.1 init (§4.1)

| # | Command | Expected | REQ |
|---|---------|----------|-----|
| 1 | `$GRAFT init` | Create `graft.toml` (`dir = "deps"`), exit 0 | REQ-INIT-DEFAULT |
| 2 | `$GRAFT init third_party` | `dir = "third_party"`, exit 0 | — |
| 3 | `$GRAFT init` (run when already exists) | `error: graft.toml already exists`, exit 2 | REQ-INIT-NOCLOBBER |
| 4 | Run `$GRAFT status` / `apply` without toml | `graft.toml not found`, exit 2 | REQ-ROOT-NOTFOUND |

### 3.2 add (§3.1, §4.2)

Run `$GRAFT init` first, then proceed item by item:

| # | Command | Expected | REQ |
|---|---------|----------|-----|
| 0 | `add github.com/min0625/mint` (no version) | Equivalent to `@latest`, resolves highest stable tag, vendor install complete, exit 0 | REQ-ADD-LATEST |
| 1 | `add github.com/uber-go/goleak@v1.2.0` | `version = "v1.2.0"` in toml, commit in lock, **vendor install complete**, exit 0 | REQ-ADD-TAG |
| 2 | `add github.com/uber-go/goleak@latest` | Select highest non-pre-release tag (v1.3.0), update in place (toml + lock + vendor) | REQ-ADD-LATEST |
| 3 | Re-run `add ...@v1.3.0` | no-op, print `already at`, exit 0 | REQ-ADD-NOOP |
| 4 | `remove mint`, then `add github.com/min0625/mint@main` | Fresh branch-ref entry → pseudo-version `v0.0.0-<ts>-<sha12>`. (The `remove` first matters: on an *existing* entry, when `main` resolves to the already-locked commit the "already at" no-op wins and `version` keeps the tag — per spec §4.2.) | REQ-ADD-PSEUDO |
| 5 | `add github.com/uber-go/nilaway` | **exit 2**, error includes symlink path `AGENTS.md`; toml does not keep partial entries | REQ-HASH-SYMLINK-PATH |
| 6 | `add github.com/uber-go/nilaway --symlinks=skip` | Print warning for each symlink, success, entry writes `symlinks = "skip"` | REQ-ADD-SYMLINKS, REQ-DEP-SYMLINKS |
| 7 | `add github.com/min0625/mint@v0.0.6 --subdir internal --name mint-internal` | Install subdirectory only, exit 0 | — |
| 8 | `add github.com/uber-go/goleak@v1.1.12 --name vendored/old-goleak` | Nested name, install in `deps/vendored/old-goleak` | — |
| 9 | `add github.com/uber-go/goleak@8186b79 --name g2` | Partial SHA → resolve to pseudo-version | — |
| 10 | `add github.com/min0625/this-does-not-exist` | **exit 3** (repo unreachable) | REQ-EXIT-NET |
| 11 | `add github.com/uber-go/goleak@v99.99.99 --name goleak` | **exit 1** (reachable but ref does not exist); needs `--name` because #8/#9 already gave this repo multiple entries | — |
| 12 | Check `graft.toml` | Sorted by `name`; existing per-entry/inline comments preserved | REQ-ADD-PRESERVE |

**Name Conflict Resolution** (§4.2, REQ-ADD-RESOLVE):

| # | Scenario | Expected |
|---|----------|----------|
| 13 | Two entries for same repo, add again without `--name` | **exit 2**: `declared by multiple entries: ...`, requires `--name` to clarify |
| 14 | Default derived name conflicts with existing entry of a different repo | **exit 2**: `name "X" is already taken by an entry for <repo>` |
| 15 | `--name X` conflicts with existing entry of a different repo | **exit 2** (same as above); graft never silently re-points entries to other repos |

> Row 15 is a regression point fixed in 2026-06: earlier versions would silently re-point; ensure this is enforced.

> **T14 Setup Note**: If the target repo is already registered under any name in toml, graft finds the existing entry by repo URL and updates it — it will not derive a new name and will not trigger a conflict. To reproduce T14, ensure the target repo (e.g. `github.com/min0625/mint`) has **no entries in toml yet**, and the derived name (`mint`) is already taken by a different repo.

| # | Command | Expected | REQ |
|---|---------|----------|-----|
| 16 | `add <repo>@<ref>` where `<ref>` is a tag containing a double quote, backslash, or control character (git allows these in tag names; `graft.toml`'s surgical writer cannot escape them) | **exit 2** after resolution but before any file is written; `graft.toml` still parses afterward | REQ-TOML-SAFE |

### 3.3 remove (§4.1)

| # | Command | Expected | REQ |
|---|---------|----------|-----|
| 1 | `remove <name>` | Delete toml + lock entry and vendor directory, exit 0 | — |
| 2 | `remove does-not-exist` | **exit 2**, suggest using `graft status` to check names | REQ-REMOVE-MISSING |

### 3.4 lock (§4.1, §4.3)

| # | Command | Expected | REQ |
|---|---------|----------|-----|
| 1 | `lock` | Re-generate lock from toml, do not install vendor, exit 0 | REQ-LOCK-RESYNC |
| 2 | Manually add a dep to toml then `lock` | Resolve new entry and write to commit | REQ-LOCK-RESYNC |
| 3 | `lock --check` (when synced) | exit 0, print `✓ graft.lock is up to date` | REQ-LOCKCHECK-INSYNC |
| 4 | `lock --check` after changing toml version | **exit 2**, list out-of-sync dep names | REQ-LOCKCHECK-OUTOFDATE |
| 5 | `lock --check` after deleting lock | **exit 2** | REQ-LOCKCHECK-MISSING |

### 3.5 apply (§4.4)

| # | Scenario | Expected | REQ |
|---|----------|----------|-----|
| 1 | `apply` without lockfile | **exit 2** | REQ-APPLY-NOLOCK |
| 2 | `apply` when toml/lock out of sync | **exit 2**, print version differences | REQ-APPLY-SYNC |
| 3 | `apply` after clearing vendor | Re-install missing, exit 0 | REQ-APPLY-RECONCILE |
| 4 | `apply` when already synced | no-op, print `already up to date` | REQ-APPLY-NOOP |
| 5 | `apply` after manually modifying vendor file content | Re-install to repair the dep | REQ-APPLY-REPAIR |
| 6 | `apply` after changing toml `symlinks` | **exit 2** (regardless of whether store is warm) | REQ-APPLY-SYMLINKS-SYNC |

### 3.6 status (§4.5)

Induce drift and verify status column and exit code:

| # | Method | Expected Status | exit |
|---|--------|-----------------|------|
| 0 | Run `status` after `$GRAFT init` without adding any dep | `✓ no dependencies` | 0 |
| 1 | All ready | All `ok` | 0 |
| 2 | Append content to a dep file | `modified` | 1 |
| 3 | `rm -rf` a dep vendor directory | `missing` | 1 |
| 4 | Manually create extra directory under `<dir>` | `extra` (commit column shows `-`) | 1 |
| 5 | A dep exists in lock but not in toml | `out of sync` (commit column `-`) | 2 |
| 6 | Simultaneously have a missing dep (T3) and an out-of-sync dep (T5) | Both rows printed; exit takes the higher code | **2** |
| 7 | Change a dep's `symlinks =` policy in toml without re-locking | `out of sync` (same sync-key set as `apply`/`lock --check`, so an all-`ok` status implies `apply` will not refuse to run) | 2 |

> Row 5 (toml↔lock out of sync) returns **2**, matching `lock --check`/`apply`;
> pure vendor drift (missing/modified/extra) is still 1. When multiple occur,
> the highest code wins (T6).

REQ: REQ-STATUS-STATES, REQ-STATUS-EXIT, REQ-STATUS-SYMLINKS-SYNC. status is read-only, no network.

### 3.7 cache (§4.7)

| # | Command | Expected |
|---|---------|----------|
| 1 | `cache dir` | Print `$GRAFT_CACHE_DIR`; directory structure is `links/locks/repos/store/tmp` plus a `CACHEDIR.TAG` marker (backup tools skip it; `cache clean` requires it before deleting) |
| 2 | `cache verify` | Re-hash all store entries, exit 0 if clean |
| 3 | Tamper with a store entry file then `cache verify` | **exit 4**, remove corrupted entry; verify again to be clean. **Note**: store files are read-only by default (mode 444); run `chmod u+w <file>` before tampering |
| 4 | Tamper with a store entry file, delete the vendor dir, then `apply` | **exit 4** ("cached content … is corrupted"), nothing installed, the corrupted entry is removed; a second `apply` re-fetches and succeeds (REQ-STORE-INSTALL-VERIFY) |
| 5 | `cache prune` | Selective reclaim: remove store entries "unreferenced by any link and unused for 30+ days" plus bare repos not fetched for 30+ days, report space freed, exit 0; prints `✓ cache already clean` when nothing to reclaim |
| 6 | `cache clean` | Wipe the entire cache (every bare repo and store entry), report space freed |

> **T3/T4 prerequisite**: confirm the target store entry actually exists before
> tampering with it. If `cache clean` ran earlier (or `GRAFT_CACHE_DIR` is
> fresh), the store is empty; if the vendor directory still matches the
> lockfile at that point, `apply` is a no-op and will **not** repopulate the
> store (copy mode does not re-diff vendor content against the store on every
> run). To guarantee the entry exists, `rm -rf` that dep's vendor directory
> and run `apply` once to force a fresh install before tampering — or locate
> the entry directly via the dep's hash in `graft.lock`, at
> `$GRAFT_CACHE_DIR/store/sha256/<first 2 chars>/<remaining 62 chars>/`.

## 4. Advanced Scenarios

### 4.1 Integrity and Security (§3.2, §7)

```bash
# Tamper with lockfile hash → apply must exit 4 (REQ-INTEGRITY)
ZEROHASH="sha256:0000000000000000000000000000000000000000000000000000000000000000"
sed -i "s/hash = \"sha256:[0-9a-f]*/hash = \"${ZEROHASH}/" graft.lock
$GRAFT apply; echo "exit=$?"   # Expected 4, print expected/got

# Malformed lockfile field → exit 2 before any install, never a panic (REQ-LOCK-VALIDATE)
sed -i 's/hash = "sha256:[0-9a-f]*"/hash = ""/' graft.lock         # empty hash
$GRAFT apply; echo "exit=$?"   # Expected 2, "invalid graft.lock", NOT a Go panic
sed -i 's/commit = "[0-9a-f]*"/commit = ""/' graft.lock            # empty commit
$GRAFT apply; echo "exit=$?"   # Expected 2
$GRAFT lock --check; echo "exit=$?"  # Expected 2 — load-time validation runs here too

# Path traversal is always rejected with exit 2
$GRAFT add <repo> --name '../escape'      # exit 2
$GRAFT add <repo> --name '/abs/path'      # exit 2
$GRAFT add <repo> --subdir '../../etc' --name p  # exit 2
$GRAFT add <repo> --name '.graft-tmp'     # exit 2 (reserved .graft- prefix — collides with staging dir)
$GRAFT add <repo> --name '.graft-tmp/x'   # exit 2
$GRAFT add <repo> --name '.GRAFT-TMP'     # exit 2 (case-insensitive: collides on case-insensitive filesystems)
$GRAFT add <repo> --name '.graft-cache'   # exit 2 (whole .graft- prefix is reserved)
# A "."-bearing name is fine — only the reserved prefix is rejected
$GRAFT add github.com/min0625/mint --name 'github.com/min0625/mint'  # ok
$GRAFT add <repo> --name '.graft'         # ok (bare .graft, no hyphen, is not reserved)
```

### 4.2 Link Mode (§5.4) and Cache Clean Interaction (§4.7)

```bash
GRAFT_LINK_MODE=symlink $GRAFT add github.com/uber-go/goleak@v1.3.0
readlink deps/goleak                       # Points to store/sha256/...
$GRAFT cache clean                          # Clear → symlink becomes dangling
GRAFT_LINK_MODE=symlink $GRAFT status       # missing, exit 1
GRAFT_LINK_MODE=symlink $GRAFT apply         # Re-materialize (re-fetch), exit 0
$GRAFT status                                # Mode drift: link dest checked in copy mode → modified, exit 1 (REQ-STATUS-MODE-DRIFT)
GRAFT_LINK_MODE=bogus  $GRAFT apply          # exit 2 (unsupported mode)
GRAFT_LINK_MODE=bogus  $GRAFT status         # exit 2 too — status judges dests by mode, so it validates it as well
```

### 4.3 Concurrency and Advisory Lock (§5.4, §5.5)

```bash
# Two adds running simultaneously: one should print "waiting for another graft process to finish…"
# and serialize; after completion, both entries should be correctly written, lock --check passes.
$GRAFT add github.com/uber-go/goleak@v1.3.0 --name a &
$GRAFT add github.com/uber-go/goleak@v1.2.0 --name b &
wait
$GRAFT lock --check; echo "exit=$?"   # 0
```

### 4.4 Replacing a Dest the Shell Is Inside (§5.1)

```bash
# Tamper the dep so apply must reinstall it, then cd into its own vendor dir before re-running.
echo "tampered" > deps/scripts/run.sh
cd deps/scripts
$GRAFT apply; echo "exit=$?"   # exit 1, "cannot replace ... current directory is inside it"
cd - && $GRAFT apply; echo "exit=$?"   # exit 0 once the shell is back out
```

### 4.5 Credential-Prompt Suppression (§7)

```bash
# A stub `git` placed ahead of the real one on PATH records the environment and
# arguments graft invokes it with, without needing an actual private repo to
# test against. Resolve the real git path *before* overriding PATH below, and
# bake the absolute path into the stub — hardcoding /usr/bin/git would break on
# machines where git lives elsewhere (e.g. Homebrew on macOS), and looking it
# up inside the stub itself would just find the stub again.
mkdir -p /tmp/stubbin
REAL_GIT=$(command -v git)
cat > /tmp/stubbin/git <<EOF
#!/bin/sh
{ env | grep -E '^GIT_TERMINAL_PROMPT='; echo "ARGS: \$*"; } >> /tmp/git-env.log
exec "$REAL_GIT" "\$@"
EOF
chmod +x /tmp/stubbin/git

rm -f /tmp/git-env.log
PATH=/tmp/stubbin:$PATH $GRAFT add github.com/uber-go/goleak@v1.3.0
grep -q '^GIT_TERMINAL_PROMPT=0$' /tmp/git-env.log && echo ok   # every git invocation disables the terminal prompt
grep -q -- '-c credential.interactive=false' /tmp/git-env.log && echo ok   # and disables credential-helper prompts too
```

## 5. Custom Fixtures Required (Not covered in this manual, suggest using local `file://` repos)

- **Skip pre-release tags**: `@latest` should skip `-rc`/`-alpha`; requires a repo where the "highest tag is a pre-release."
- **Unicode NFC/NFD and case-folding path collisions**: Requires a tree containing colliding paths; expect **exit 2** (REQ-HASH-UNICODECOLLIDE, REQ-HASH-CASECOLLIDE).
- **Exec-bit**: The semantic is "upstream git mode changes (reflected via re-fetch to `.graft-execbits`) will be detected as drift"; **local `chmod` on vendor files will not be detected** (exec status is taken from metadata files, not filesystem mode, which is by design). Verifying upstream mode changes requires two versions of a tree.
- **Git submodule (gitlink) rejection**: A repo containing a `160000` entry (`git submodule add`, or `git update-index --add --cacheinfo 160000,<sha>,<path>`); `add`/`lock` must fail with **exit 2** naming the entry's path (REQ-FETCH-GITLINK).
- **Symlink policy on Windows**: The symlink policy is decided from git tree modes before checkout, so `reject`/`skip` and the content hash behave identically on Windows (where `core.symlinks=false` makes git check out a symlink as a plain placeholder file) and on POSIX. Verify a lockfile produced on Linux for a `symlinks = "skip"` dep applies cleanly on Windows and vice versa (REQ-FETCH-SYMLINK-MODE).
- **Deep paths on Windows**: A tree whose paths exceed 260 characters must lock and install on Windows — graft forces `core.longpaths=true` on its checkouts (REQ-FETCH-LONGPATHS). Path collisions are likewise detected from tree paths before checkout, so they are caught even on case-insensitive filesystems (REQ-FETCH-PATHCHECK).

## 6. Cleanup

```bash
rm -rf "$GRAFT_CACHE_DIR"     # Cache deletion is always safe
# Remove temporary project directories (created by mktemp -d)
```

---

When adding new specifications, please update this manual with the corresponding black-box scenarios.
