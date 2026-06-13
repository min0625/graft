# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What graft is

A language-agnostic dependency manager for git repositories (Go, single binary) — a replacement for git submodules. Users declare deps in `graft.toml`, graft resolves them go.mod-style (tags or pseudo-versions) and pins exact commit SHAs plus content hashes in `graft.lock`; `graft apply` reconciles the vendor directory to match the lockfile exactly.

**Current status: Milestones 1–4 implemented** — the full CLI (`init`/`add`/`remove`/`apply`/`lock`/`status`/`cache`), parallel apply, the per-project advisory lock, golden output tests, the release pipeline (`.goreleaser.yaml`, `install.sh`), and the global cache layer: the shared bare-repo cache (`internal/repocache`), the content-addressed store (`internal/store`, with reflink + `graft cache verify`/`clean`), and link mode (`graft apply --link`/`GRAFT_LINK_MODE`, registered in `internal/links`). Milestone 5 (ecosystem docs) is not implemented yet; the README marks that section as planned. Two Milestone-3 items (T3.4 release tag, T3.5 Homebrew cask) have code in place but await external actions (pushing the first tag, adding the tap secret). The source of truth is the design spec in `.local.design-spec.zh-TW.md` (gitignored) — read it before implementing anything. Docs that must stay consistent with it: `.local.tasks.zh-TW.md` (task breakdown with progress checkboxes), `README.md`, `README.zh-TW.md`. When changing behavior or design, update all of them.

## Commands

Tool versions are managed by [mise](https://mise.jdx.dev/) ([mise.toml](mise.toml)): Go 1.26.4, golangci-lint 2.12.2.

```bash
make check        # everything CI runs: check-tidy + lint + test
make test         # go test -race -failfast -v ./...
make lint         # golangci-lint config verify + run
make fix          # go mod tidy + golangci-lint --fix
make check-tidy   # go mod tidy -diff

# single test
go test -race -run TestName ./internal/somepkg/
```

Lint only checks code changed since `NEW_FROM_REV` (default `HEAD`); CI runs `make check NEW_FROM_REV=origin/main`. To lint the whole repo, run `golangci-lint run ./...` directly.

## Planned architecture (design spec §5)

CLI layer in `cmd/` (cobra: `init`, `add`, `remove`, `apply`, `lock`, `status`, `cache`) on top of `internal/` packages:

- `config` — `graft.toml` manifest read/write
- `lockfile` — `graft.lock` read/write (`lock_version`, per-dep `commit`/`time`/`hash`)
- `resolver` — ref resolution (tag / branch / partial SHA → full SHA; no ref → latest semver tag)
- `fetcher` — git clone/fetch into a per-user bare-repo cache, with a 3-step fallback fetch strategy (shallow SHA fetch → tag fetch → full fetch)
- `hasher` — sha256 content-tree hashing for the lockfile and the global content store
- `vendordir` — vendor directory reconciliation (parallel worker pool, atomic same-filesystem renames staged in `<vendor>/.graft-tmp`, copy/reflink or symlink/junction link mode)

Key invariants from the spec:

- Installs only ever use the locked commit SHA + content hash — a moving branch or re-pointed tag must never change what gets installed.
- `graft apply` never modifies the lockfile; `graft status` is read-only and makes no network requests.
- Mutating commands take a per-project advisory lock stored in the global cache (`GRAFT_CACHE_DIR`), never in the repo.
- The global cache (bare repos + content store keyed by hash) is purely a performance layer — deleting it must always be safe.
- Defined exit codes (spec §4.5): e.g. 2 = lockfile missing/out of sync, 4 = integrity (hash) failure.

## Conventions

- Every Go file starts with the license header: `// Copyright 2026 The Graft Authors` (the `goheader` linter is enabled).
- The golangci-lint config ([.golangci.yaml](.golangci.yaml)) is strict and curated: gofumpt + golines (120 cols), `wsl_v5`, `testpackage` (tests go in `_test` packages), `interface{}` is rewritten to `any`. Run `make fix` before committing.
- The project is still open to design changes — better alternatives to the current spec are welcome to propose, but keep all documents (spec, translations, READMEs, tasks) in sync when anything changes.
