# 0004 — JSON output contract

mbx emits JSON by default on stdout for every command. Output uses a versioned envelope:

```json
{ "v": 1, "data": <command-specific>, "meta": { "account": "work", "next_cursor": "...", "errors": {...} } }
```

Errors are JSON on stderr with the same `v` and a structured `error` object: `{ "v": 1, "error": { "code": "auth.refresh_failed", "message": "...", "details": {...} } }`. Exit codes are grouped: `0` success, `1` generic, `2` invalid usage, `10–19` auth, `20–29` provider, `30–39` cache, `40–49` config. `-o table` opts in to human output; there is no TTY-detection.

The `code` field is the stable interface — codes don't change without bumping `v`. The `message` field is human-facing and may evolve. Adding new optional fields under `data`/`meta`/`error.details` is backward-compatible and does not bump `v`. Removing or renaming fields, or changing semantics of an existing field, requires a major mbx version and a bump to `v`.

## Considered alternatives

- **TTY-detect default (imbox).** Rejected: scripts running mbx inside subshells with a fake TTY get human output and crash. Agents running inside terminal sessions can hit the same trap. Always-JSON is one consistent contract.
- **No envelope, raw arrays/objects.** Rejected: gives no place to surface pagination cursors, partial-failure errors from fan-out (`-a a,b`), or the resolved account. Also no version lever.
- **Per-call API version selector (`--api-version 1` / `$MBX_API_VERSION`).** Rejected: over-engineered for a single-user CLI. A clean major-version bump of mbx itself is the right granularity.

## Consequences

- Every list verb (`envelope list`, `envelope search`, `attachment list`, ...) carries a `meta.next_cursor`. Cursors are opaque to skills; mbx encodes whatever the provider needs (Gmail page tokens, IMAP UID ranges).
- Fan-out (`-a a,b`) succeeds partially by default: data from succeeding accounts in `data`, failures keyed by account in `meta.errors`, exit 0. `--strict` flips to all-or-nothing.
- `--limit N` is per-account, not total. Documented in the README.
- Skills assert `v == 1` and refuse to run otherwise. mbx publishes the code taxonomy and exit-code map as part of the README, treated as public surface.
