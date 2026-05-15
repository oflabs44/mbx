# Changelog

All notable changes to mbx are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
mbx aims for [semantic versioning](https://semver.org/spec/v2.0.0.html) once
v1.0.0 ships. Pre-v1 releases may include breaking changes between minor
versions — they will be called out in the relevant entry's "Changed"
section.

## [Unreleased]

### Added
- `mbx message archive <id>...` verb. On Gmail removes the `INBOX` label;
  on IMAP moves to `folder.aliases.archive` (`config.invalid` if unset).
  `archive` joins `inbox`, `sent`, `drafts`, `trash` as a canonical folder
  role. See [ADR-0009](./docs/adr/0009-archive-verb-and-canonical-role.md).

### Fixed
- `mbx message move` and `message copy` on Gmail now accept user-defined
  label names (e.g. `Newsletters`). Previously only Gmail system labels
  (`TRASH`, `SPAM`, …) worked; user labels failed with
  `gmail (400): Invalid label`. mbx now resolves the display name to the
  opaque label id before the modify call (issue #1). Unknown destinations
  surface as `provider.not_found` with `details.name`.

### Documentation
- `docs/commands.md` no longer advertises a non-existent Gmail "Archive"
  destination for `message move`; the example points at `mbx message
  archive` instead.

## [0.1.1]

### Added
- JSON Schema for `config.toml` at [`docs/config.schema.json`](./docs/config.schema.json).
  Editors and agents (Helix / Zed / VS Code via `taplo`) can validate field
  names, types, enums, secret-block `oneOf` discipline, and the gmail/imap
  shape divergence without running mbx. Semantic checks (live OAuth refresh,
  IMAP login, secret resolution) remain owned by `mbx account doctor`.

### Fixed
- `mbx version` now reports the real build version. The Makefile injects
  `git describe --tags --dirty --always` via `-ldflags`; previously every
  `make build` / `make install` produced a binary that printed `"dev"`.

### Documentation
- README trimmed to the user-facing surface; project-internal detail moved
  out of the entry doc.

## [0.1.0]

First tagged release. Covers phases 0–7 of the implementation plan plus
phase 8 polish: lint/test CI, shell completion, complete error-code
taxonomy, per-command examples.

### Added
- Account lifecycle — `mbx account list | add | auth | doctor | remove | rename`,
  with OAuth (Google), IMAP password, and per-account alias support for safe
  renames (ADR-0007).
- Gmail read path — `envelope list | search | thread`, `message read | export`,
  `folder list`, `attachment list | download` via the Gmail HTTP API.
- IMAP read path — same surface against `emersion/go-imap/v2`, including
  Proton via the local bridge and corporate IMAP+OAuth.
- Write path — `envelope flag` (cross-provider vocabulary), `message move |
  copy | delete | send | reply | forward`, folder `add | delete | expunge |
  purge`. Multi-ID inputs are fail-fast; idempotent verbs document that.
- Threading — Gmail native; IMAP server `THREAD=REFERENCES` with bounded-
  window client fallback when the server doesn't advertise it.
- Cache — opt-in SQLite store (pure-Go `modernc.org/sqlite`) at
  `<cache-dir>/cache.db`, keyed by canonical account name (ADR-0008).
  `mbx cache sync | list | search | status | clear`. Mutating live verbs
  write through best-effort; `account rename` rekeys cache rows.
- Multi-account fanout — `-a a,b,c` on `envelope list | search` (and the
  cache equivalents). Default is partial success with per-account errors
  in `meta.errors`; `--strict` fails fast.
- Stable JSON contract — every command emits `{"v":1, ...}` on stdout
  (success) or stderr (error). No TTY detection (ADR-0004).
- Stable error codes — `auth.*`, `provider.*`, `cache.*`, `config.*`,
  `fanout.*`, plus `input.*` / `usage.invalid`. Full table in
  [`docs/commands.md`](./docs/commands.md#exit-codes).
- Shell completion — `mbx completion bash|zsh|fish|powershell`.
- CI — GitHub Actions runs gofmt, `go mod tidy`, `go vet`, tests, and a
  build on every PR + push to main.
- Distribution — `git clone && make install`. No pre-built binaries; the
  build is pure-Go and cross-compiles cleanly.

### Documentation
- [`README.md`](./README.md) — overview + doc index.
- [`CONTEXT.md`](./CONTEXT.md) — domain language (Envelope, Folder, Flag,
  mbx ID, Cache, Write-through, Secret, write_cmd).
- [`docs/commands.md`](./docs/commands.md) — command surface contract.
- [`docs/config.md`](./docs/config.md) — TOML config reference.
- [`docs/adr/`](./docs/adr/) — eight architecture decision records:
  secrets-resolution, self-describing IDs, cache-as-derived-state,
  JSON output contract, TOML parser choice, himalaya config shape,
  account aliases, cache storage and schema.

[Unreleased]: https://github.com/oflabs44/mbx/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/oflabs44/mbx/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/oflabs44/mbx/releases/tag/v0.1.0
