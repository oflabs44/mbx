# 0003 — Cache is derived state; live-first; best-effort write-through

mbx is live-first: every command queries the provider API by default. A per-account SQLite cache is opt-in (config block `cache = { path = "...", sync_days = N, folders = [...] }`) and accessed *only* via `mbx cache *` subcommands. Live verbs (`envelope list`, `message read`, ...) never read from cache.

After a successful mutating remote write (`envelope flag`, `message move`, `message delete`), mbx best-effort updates the corresponding cache row. Cache-update failure is logged at `--verbose` and never blocks command exit. `send`, `reply`, `forward` do not write-through.

This combination keeps the cache as a true derived/optional concern — it can be deleted and rebuilt at any time with `mbx cache sync` — while keeping cached views consistent with the agent's most recent action 99% of the time.

## Considered alternatives

- **Cache-first (mu/notmuch/aerc model).** Rejected: requires a sync engine (or external mbsync) and shifts mbx from a one-shot CLI to a stateful tool. Wrong for the agent use case, where each invocation should see *current* state by default.
- **`--offline` flag on every read verb.** Rejected: leaks the cache concept into every command surface and creates a "did this answer come from cache or remote?" ambiguity. Scoping cache reads to their own verb-tree (`mbx cache list`, `mbx cache search`) makes the mental model crisp.
- **Authoritative write-through (block on cache-write success).** Rejected: makes the cache part of the critical path. Any local SQLite hiccup turns into a write failure for the user.
- **Invalidation on write (delete cache row).** Rejected: leaves holes in the cache that complicate iteration and ranking.
- **Dirty-marker / write-behind.** Rejected: introduces "pending" as a new state agents must understand.

## Consequences

- `mbx cache status` exposes `last_sync_at`, `rows`, and `drift_detected` (rows the most recent write-through failed to update).
- Cache schema migrations are not supported: a major mbx version bump can simply require `mbx cache clear && mbx cache sync`. The cache is throwaway.
- The "cache schedule" decision is deferred to the user — mbx provides no scheduler. Recipes for `cron`, `launchd`, and systemd timers go in the README.
- An agent that wants live data uses `envelope`/`message` verbs. An agent that explicitly wants cached data (e.g. analytics over months of history) uses `cache` verbs. Both are first-class; neither overlaps.
