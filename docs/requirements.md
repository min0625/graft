# graft — Requirements (traceability matrix)

This file is the formal, machine-readable layer of the design spec
([`design.zh-TW.md`](design.zh-TW.md) / [`design.md`](design.md)). Each row is one
normative requirement with a stable ID, a one-line statement, and the spec
section it derives from. The prose specs explain *what and why*; this table is
the *testable contract*.

**How it is enforced.** `internal/clierr/spec_coverage_test.go` parses every
`REQ-…` ID below and scans every `*_test.go` file for `REQ-…` references (by
convention written in a `// spec: REQ-…` comment next to the covering test). It
fails if:

- a requirement here is referenced by no test (the requirement is unverified), or
- a test references a `REQ-…` ID that does not exist here (a typo or stale ID).

So adding a row to this table is a ratchet: the build stays red until a test
covers it. To verify the tool's behavior against the spec, a human reads a row,
follows its ID to the annotated test, and reads what that test asserts.

**ID scheme.** `REQ-<AREA>-<TOKEN>` — uppercase area, uppercase/digit token.
IDs are append-only: never renumber or reuse a retired ID.

**Coverage status.** This table currently covers the core install loop
(milestones 1–2 behaviors). Remaining sections of the spec are filled in
incrementally; each addition forces a covering test.

| ID | Requirement | Spec |
|----|-------------|------|
| REQ-INIT-DEFAULT | `graft init` with no argument creates `graft.toml` with `dir = "deps"` as the default install root. | §4.1 |
| REQ-INIT-NOCLOBBER | `graft init` fails with exit code 2 when `graft.toml` already exists; it never overwrites. | §4.1 |
| REQ-ROOT-NOTFOUND | Commands other than `init`/`cache` fail with exit code 2 and a "graft.toml not found" hint when no manifest is found. | §4.1 |
| REQ-ADD-TAG | A tag ref is written verbatim as `version` in `graft.toml`, and its commit SHA is locked in `graft.lock`. | §3.1, §4.2 |
| REQ-ADD-NOOP | Re-adding a dependency that resolves to the same commit is a no-op that prints an "already at" line. | §4.2 |
| REQ-ADD-PSEUDO | A branch or SHA ref with no tag produces a go.mod-style pseudo-version in `graft.toml`. | §3.1, §4.2 |
| REQ-ADD-LATEST | `@latest` / an omitted ref with no matching SemVer tag falls back to the remote `HEAD` with a pseudo-version. | §4.2 |
| REQ-ADD-RESOLVE | Ambiguous or name-colliding entry resolution fails with exit code 2 before any network access. | §4.2 |
| REQ-REMOVE-MISSING | `graft remove <name>` fails with exit code 2 when the name is not in `graft.toml`. | §4.1 |
| REQ-LOCK-SHA256-COMMIT | `commit` in `graft.lock` is a non-empty hex string; both 40-char (SHA-1) and 64-char (SHA-256) values are accepted without error. | §3.2 |
| REQ-LOCK-RESYNC | `graft lock` regenerates `graft.lock` from `graft.toml` without installing into vendor. | §4.1 |
| REQ-LOCKCHECK-INSYNC | `graft lock --check` exits 0 and prints "✓ graft.lock is up to date" when every manifest entry matches its locked counterpart. | §4.3 |
| REQ-LOCKCHECK-MISSING | `graft lock --check` exits 2 when `graft.lock` does not exist. | §4.3 |
| REQ-LOCKCHECK-OUTOFDATE | `graft lock --check` exits 2 and lists out-of-date dep names when any entry's `repo`, `version`, or `path` differs between manifest and lockfile, or an entry exists in only one of them. | §4.3 |
| REQ-LOCKCHECK-NOWRITE | `graft lock --check` never modifies `graft.lock` or any other file. | §4.3 |
| REQ-APPLY-NOLOCK | `graft apply` exits 2 when `graft.lock` is missing. | §4.4 |
| REQ-APPLY-SYNC | `graft apply` exits 2 when `graft.lock` is out of sync with `graft.toml`. | §4.4 |
| REQ-APPLY-RECONCILE | `graft apply` brings the vendor directory to the locked state (installs missing dependencies). | §4.4 |
| REQ-APPLY-NOOP | `graft apply` is a no-op that prints "already up to date" when vendor already matches the lock. | §4.4 |
| REQ-APPLY-REPAIR | `graft apply` re-installs a hand-edited vendor tree so it matches the locked hash. | §4.4 |
| REQ-STATUS-STATES | `graft status` reports `ok` / `missing` / `modified` / `extra` / `out of sync` per dependency. | §4.5 |
| REQ-STATUS-EXIT | `graft status` exits 0 when everything is `ok`, and exits 1 on any drift. | §4.5, §4.6 |
| REQ-EXIT-NET | A remote that cannot be reached fails with exit code 3 (network error). | §4.6, §5.5 |
| REQ-INTEGRITY | A content-hash mismatch fails with exit code 4 (content integrity failure). | §4.6, §7 |
| REQ-PARALLEL-COLLECT | The parallel reconcile collects and reports every dependency's error, rather than failing fast on the first. | §5.4 |
| REQ-JOBS-FETCHDEFAULT | The fetch phase runs up to 16 concurrent workers, which can exceed `runtime.NumCPU()`; the install phase runs up to `runtime.NumCPU()` workers. | §5.4 |
| REQ-JOBS-SAMEREPO | Multiple deps sharing the same bare-repo cache entry are serialized by the per-repo advisory lock. | §5.4, §5.6 |
| REQ-HASH-EXECBIT | The executable bit (git mode 100755 vs 100644) is included in each file's hash input as a single exec byte; a chmod-only change is detected as drift (exit 4) and `graft apply` re-installs. | §3.2 |
| REQ-HASH-CASECOLLIDE | A fetched tree containing two paths that are identical after Unicode case-folding is rejected with exit code 2. | §3.2 |
| REQ-HASH-UNICODECOLLIDE | A fetched tree containing two paths that are identical after Unicode NFC/NFD normalization is rejected with exit code 2. | §3.2 |
| REQ-ADD-PRESERVE | `graft add` and `graft remove` edit `graft.toml` in place; comments and the relative order of unchanged entries are preserved. | §3.1 |
| REQ-HASH-SYMLINK-PATH | Symlink rejection error message includes the symlink's relative path within the dependency tree. | §3.2 |
| REQ-DEP-SYMLINKS | `symlinks = "skip"` on a dep causes symlinks to be silently skipped (excluded from hash, not copied to vendor) and a warning printed per skipped symlink; the dep installs successfully. | §3.1, §3.2 |
| REQ-ADD-SYMLINKS | `graft add --symlinks=skip <repo>` adds a repo containing symlinks in one shot, writing `symlinks = "skip"` to the dep's `graft.toml` entry. | §3.1, §4.2 |
