# 0008 — Cache storage: pure-Go SQLite, single DB, fixed schema, no migrations

[ADR-0003](./0003-cache-as-derived-state.md) established that the cache is opt-in derived state — never authoritative, rebuildable from scratch. This ADR pins the concrete storage choices Phase 6 needs: which SQLite driver, where the DB lives, which tables, and how schema evolution is handled.

## Driver: `modernc.org/sqlite` (pure Go)

mbx ships as a static binary across darwin/linux × amd64/arm64 (Phase 8 release matrix). Pure-Go SQLite removes cgo from the build, keeping `go build` and CI cross-compilation trivial and the binary self-contained.

The cgo alternative (`mattn/go-sqlite3`) is faster on heavy workloads but irrelevant here — the cache holds thousands of rows, not millions, and every query is single-user. The build and distribution simplification dominates.

## One DB for all accounts

A single SQLite file at `<cache-dir>/cache.db` holds every cached account. Each row's `account` column carries the canonical account name; cross-account queries (`mbx cache list -a personal,work --unread`) become `WHERE account IN (...)` against one DB instead of a Go-side fanout-and-merge across N DB files.

This aligns with [ADR-0007](./0007-account-renames-via-aliases.md)'s "canonical-on-emit" rule: the row key (mbx ID) and the `account` column are both stamped with the canonical name; rename promotes alias rows to the new canonical via a single `UPDATE` (see "Rename interaction" below).

### `<cache-dir>` resolution

Mirrors `DefaultConfigDir` so the env-var-layering convention stays consistent across mbx's two filesystem roots:

1. Config TOML — `cache-dir = "..."` if set (user explicit, per-project override).
2. `$MBX_CACHE_DIR` — app-level env override.
3. `$XDG_CACHE_HOME/mbx` — platform standard.
4. `$HOME/.cache/mbx` — universal fallback. mbx does **not** use `os.UserCacheDir` because on macOS it returns `~/Library/Caches`, wrong for CLI tooling (same reason `DefaultConfigDir` avoids `os.UserConfigDir`).

The directory is created (`MkdirAll 0700`) on first cache verb that needs to write.

### `cache.path` is gone

The previously-documented per-account `cache.path = "..."` field is removed. The validator rejects it at load with a `config.invalid` (exit 40) error that names the field and points at `cache-dir`. Per-account `cache = { sync_days, folders }` stays — that's the opt-in marker + sync tuning.

## Schema

```sql
CREATE TABLE envelopes (
  id            TEXT PRIMARY KEY,    -- canonicalized mbx ID
  account       TEXT NOT NULL,       -- canonical account name; indexed for WHERE account IN (...)
  thread_id     TEXT,
  from_addr     TEXT,
  to_addrs      TEXT,                -- comma-joined; cache is presentation-shape, not normalized
  cc_addrs      TEXT,
  subject       TEXT,
  date          INTEGER NOT NULL,    -- unix seconds, indexed for ORDER BY date DESC
  snippet       TEXT,
  has_attach    INTEGER NOT NULL,    -- 0/1
  provider      TEXT NOT NULL,       -- 'gmail' | 'imap'
  gmail_labels  TEXT,                -- JSON array; gmail provider only
  synced_at     INTEGER NOT NULL
);
CREATE INDEX envelopes_account_date ON envelopes(account, date DESC);
CREATE INDEX envelopes_thread       ON envelopes(thread_id);

CREATE TABLE envelope_flags (
  envelope_id TEXT NOT NULL REFERENCES envelopes(id) ON DELETE CASCADE,
  flag        TEXT NOT NULL,         -- 'seen' | 'flagged' | 'answered' | 'draft' | 'deleted'
  PRIMARY KEY (envelope_id, flag)
);

CREATE TABLE envelope_folders (
  envelope_id TEXT NOT NULL REFERENCES envelopes(id) ON DELETE CASCADE,
  folder      TEXT NOT NULL,
  PRIMARY KEY (envelope_id, folder)
);

CREATE TABLE messages (
  id           TEXT PRIMARY KEY REFERENCES envelopes(id) ON DELETE CASCADE,
  body_plain   TEXT,
  body_html    TEXT,
  body_source  TEXT,                 -- 'text/plain' | 'text/html'
  headers_json TEXT,                 -- JSON object of opt-in headers
  synced_at    INTEGER NOT NULL
);

CREATE TABLE sync_state (
  account      TEXT NOT NULL,
  folder       TEXT NOT NULL,        -- provider-native folder name
  uidvalidity  INTEGER,              -- IMAP only; NULL on Gmail
  last_sync_at INTEGER NOT NULL,
  envelope_n   INTEGER NOT NULL,
  PRIMARY KEY (account, folder)
);

CREATE TABLE schema_meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
INSERT INTO schema_meta (k, v) VALUES ('version', '1');
```

Reasoning:

- **Composite `envelopes_account_date` index** — every cache `list`/`search` filters by `account` first, then orders by `date DESC`. The composite index serves both the filter and the sort.
- **`sync_state` keyed by (account, folder)** — same folder name can exist under multiple accounts (everyone's "INBOX"); per-account scoping is non-negotiable.
- **Flags + folders as join tables** — a flat column would force `LIKE '%seen%'` queries; cardinality is tiny (≤ 5 flags) so the join cost is negligible while the index lookup is cheap.
- **Date as Unix seconds INTEGER**, not text — for `ORDER BY date DESC LIMIT N` to use the index without parse work. The output layer converts back to RFC 3339 for JSON.
- **`messages` table is optional** — only populated when something explicitly caches a body. `mbx cache list` / `cache search` operate on `envelopes` alone.
- **Gmail-extras (`gmail_labels`) as JSON** because the values are heterogeneous; a third join table for a Gmail-only field is over-engineering.
- **`sync_state.uidvalidity` is NULL for Gmail** — Gmail's threads/messages model doesn't need it. The column existing for both providers keeps the read path branch-free.

## Canonicalize at the cache boundary

Every mbx ID written to the cache has its `Account` segment normalized to the canonical name *before* insert (so the row keyed under `gmail:personal-gmail:abc` is the only one — never a second row under the alias form `gmail:personal:abc`). Same for reads: a lookup against an alias-form ID canonicalizes first. This is enforced inside `internal/cache/` so callers don't need to remember it.

## Rename interaction

When `mbx account rename <old> <new>` runs and the cache file exists, the rename verb additionally executes:

```sql
UPDATE envelopes  SET id = <re-keyed>, account = '<new>' WHERE account = '<old>';
UPDATE sync_state SET account = '<new>' WHERE account = '<old>';
```

(`<re-keyed>` re-stamps the ID's `Account` segment in place — the rest of the ID structure is unchanged.) This keeps the canonical-on-emit invariant intact across renames without forcing the user to `cache clear && cache sync`. The cache write is best-effort: if it fails (cache disabled, file locked, schema mismatch) the rename of the TOML succeeds and the user gets a `--verbose` warning. Cache stale rows are recovered by `cache clear` + `cache sync`.

## No migrations

`schema_meta.version` is the only knob. On open, mbx compares it to the version compiled in. Mismatch → refuse to open with `cache.schema_mismatch` (exit 31) and tell the user `mbx cache clear && mbx cache sync`. Since the cache is derived and the sync is cheap, this is preferable to maintaining migration scripts for state we promise can be thrown away.

A schema bump is just: edit the DDL, bump the version constant.

## Considered alternatives

- **One DB per account (`<cache-dir>/<name>.db`).** Rejected: cross-account cache reads — a real workflow after Phase 7 — would have to fan out across N SQLite files and merge in Go, exactly the pattern we eliminated for live verbs. Per-account failure isolation is largely theoretical (SQLite corruption is almost always filesystem-level, affecting all files in the dir). The composite `(account, date)` index on a shared table gives the same query performance.
- **`mattn/go-sqlite3` (cgo).** Rejected: cgo on every build host + release-binary fattening, for no practical query-speed benefit at our row counts.
- **Boltdb / bbolt (pure-Go KV).** Rejected: works for k/v but not for the "envelopes by date range, filtered by flag, joined to folders, scoped to N accounts" query the cache `list`/`search` verbs need. SQLite handles that natively.
- **One unified table with everything inline (no joins).** Rejected: flag/folder filtering becomes string-matching, indexing becomes fragile.
- **In-row JSON columns for flags/folders.** Rejected for the same query reason. JSON in SQLite is fine for genuinely opaque data (`gmail_labels`); for queried-on columns it's a regression.
- **Versioned migrations (Goose, golang-migrate, …).** Rejected: contradicts ADR-0003's "cache is throwaway" stance. The `schema_meta.version` refusal is enough.
- **Leave the per-account `cache.path` field for backward compatibility.** Rejected: the cache hasn't shipped, so there's no compat surface to preserve. A hard rejection at load with a clear migration message is honest.

## Consequences

- `internal/cache/` becomes the home for: schema DDL, open/close, the `Store` type that wraps the `*sql.DB`, the typed methods envelope/message/sync_state use, and the canonicalize-at-boundary helper.
- `mbx cache *` verbs land on top of `internal/cache/Store`.
- `mbx cache list / search` honour Phase 7 fanout via SQL `WHERE account IN (...)` rather than a goroutine fanout — the helper in `cmd/mbx/fanout.go` is for live verbs only.
- Write-through hooks (`envelope flag`, `message move`, `message delete`) call into `internal/cache/Store` from the cmd layer; cache writes are best-effort (logged at `--verbose`, never blocking command exit per ADR-0003).
- `mbx cache status` reads `schema_meta`, aggregates over `sync_state`, and counts rows in `envelopes` grouped by account — one query per panel.
- Each `mbx cache *` invocation opens the DB fresh and closes on exit. SQLite WAL handles the rare case where two mbx invocations touch the file concurrently.
- `config.Account.Cache.Path` is removed from the typed struct; the validator emits `config.invalid` if a stale `cache.path = ...` line is present, naming the field and pointing at `cache-dir`.
- `mbx account rename` gains a cache-aware step: on success of the TOML rewrite, if the cache file exists, run the two `UPDATE` statements above. Failure is logged but does not fail the rename.
