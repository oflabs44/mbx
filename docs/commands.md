# mbx commands

Full reference for the mbx command surface. See [CONTEXT.md](../CONTEXT.md) for domain language and [docs/adr/](./adr/) for the decisions behind these shapes.

## Conventions

### Global flags

| Flag | Description |
|---|---|
| `-a, --account <name>[,<name>...]` | Account name(s). Required unless implicit in an **mbx ID** argument. Accepts repeated flag or comma list. No wildcards, no env fallback, no implicit default. |
| `-o, --output <json\|table>` | Output format. Default `json`. No TTY-detection. |
| `-c, --config <path>` | Override config file path. Default `~/.config/mbx/config.toml`. |
| `--strict` | Fanout: fail if any account in `-a a,b` fails. Default is partial success. |
| `--verbose` | Verbose stderr logs. |
| `--debug` | Debug-level stderr logs. |
| `--no-color` | Disable color in `-o table` output. |

### mbx ID format

Every envelope, message, and thread returned by mbx carries a stable, self-describing **mbx ID**. See [ADR-0002](./adr/0002-self-describing-message-ids.md).

- Gmail: `gmail:<account>:<gmail-msg-id>`
- IMAP: `imap:<account>:<folder>:<uidvalidity>:<uid>` (folder percent-encoded)

Commands that take an ID don't need `-a`; the account is parsed from the ID. If both are passed, they must agree.

### JSON envelope

All commands emit JSON on stdout by default. See [ADR-0004](./adr/0004-json-output-contract.md).

Success:
```json
{
  "v": 1,
  "data": <command-specific>,
  "meta": { "account": "work", "next_cursor": "...", "errors": {} }
}
```

Error (stderr, non-zero exit):
```json
{
  "v": 1,
  "error": {
    "code": "auth.refresh_failed",
    "message": "OAuth refresh token rejected by provider",
    "details": { "account": "gmail-personal" }
  }
}
```

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Generic error |
| `2` | Invalid usage / bad flags (`usage.invalid`, `input.missing_flag`, `input.ambiguous_body`) |
| `10–19` | Auth (`10` refresh failed, `11` missing write_cmd, `12` invalid credentials, ...) |
| `20–29` | Provider (`20` rate limited, `21` not found, `22` ID invalidated, `23` network timeout, ...) |
| `30–39` | Cache (`30` unavailable, `31` schema mismatch, ...) |
| `40–49` | Config (`40` invalid TOML, `41` unknown account, ...) |

---

## `mbx account`

Configure and inspect accounts.

### `mbx account list`

List all configured accounts.

```bash
mbx account list
```

Output:
```json
{
  "v": 1,
  "data": [
    { "name": "gmail-personal", "type": "gmail", "email": "you@gmail.com", "cache": false },
    { "name": "work", "type": "imap", "email": "you@company.com", "cache": true }
  ]
}
```

### `mbx account add <name>`

Scaffold a `[accounts.<name>]` block in the config file. Prompts for nothing — emits a commented template the user fills in.

### `mbx account auth <name>`

Run the OAuth flow for an OAuth account. Opens a browser, listens on `redirect-host:redirect-port`, captures the callback, exchanges for tokens, and persists via the configured `write_cmd`.

**Refuses to run** if `backend.auth.refresh-token.write_cmd` is unset. Error: `code = "auth.missing_write_cmd"`, exit `11`.

### `mbx account doctor <name>`

Validate config and probe the account. Checks: TOML parse, all `read_cmd`s resolve, OAuth refresh or IMAP LOGIN succeeds, folder list works, server capabilities (THREAD, IDLE, ...).

Output:
```json
{
  "v": 1,
  "data": {
    "account": "work",
    "config": "ok",
    "secrets": "ok",
    "auth": "ok",
    "connectivity": "ok",
    "capabilities": { "thread": true, "idle": true },
    "warnings": []
  }
}
```

### `mbx account remove <name>`

Comment out the `[accounts.<name>]` block in config. Does not touch secrets in external stores.

---

## `mbx folder`

Manage IMAP folders / Gmail labels surfaced as folders.

### `mbx folder list -a <acc>`

```bash
mbx folder list -a gmail-personal
```

Output:
```json
{ "v": 1, "data": [{ "name": "INBOX", "count": 1843, "unread": 42 }, ...] }
```

### `mbx folder add -a <acc> <name>`

Create folder. For Gmail, creates a user label.

### `mbx folder delete -a <acc> <name>`

Delete folder. Fails on non-empty unless `--force`.

### `mbx folder expunge -a <acc> <name>`

Permanently remove messages already flagged `\Deleted`. IMAP-specific; no-op on Gmail.

### `mbx folder purge -a <acc> <name>`

Delete *all* messages in the folder. Destructive; requires `--yes` to confirm.

---

## `mbx envelope`

Cheap operations: id + flags + a handful of headers + snippet. No body fetch.

### `mbx envelope list`

```bash
mbx envelope list -a work --folder INBOX --unread --limit 20
mbx envelope list -a work,gmail-personal --unread             # fanout
```

| Flag | Description |
|---|---|
| `--folder <name>` | Filter to one folder. IMAP defaults to `INBOX`; Gmail to `INBOX` label. |
| `--limit <n>` | Per-account limit. Default 20. |
| `--unread` / `--starred` / `--has-attachment` | Boolean filters. |
| `--from <addr>` / `--to <addr>` | Address filters. |
| `--after <date>` / `--before <date>` | ISO-8601 dates. |
| `--query "<raw>"` | Provider-native raw query (Gmail query syntax / IMAP SEARCH). Escape hatch. |
| `--cursor <c>` | Resume from a previous response's `meta.next_cursor`. |
| `--strict` | Fail entire command if any fanout account fails. |

Output:
```json
{
  "v": 1,
  "data": [
    {
      "id": "gmail:work:18f3c2a...",
      "thread_id": "gmail:work:18f3c1...",
      "account": "work",
      "from": "alice@x.com",
      "to": ["me@company.com"],
      "subject": "Q2 review",
      "date": "2026-05-14T09:33:00Z",
      "flags": ["unread"],
      "folders": ["INBOX"],
      "snippet": "Hi — wanted to share the deck...",
      "has_attachment": false,
      "provider": "gmail",
      "gmail": { "labels": ["INBOX","UNREAD","Label_42"] }
    }
  ],
  "meta": {
    "accounts_queried": ["work"],
    "next_cursors": { "work": "eyJwYWdlVG9rZW4..." },
    "errors": {}
  }
}
```

### `mbx envelope search`

Cross-folder keyword search. Same flags as `list`, plus a positional `"<keywords>"`.

```bash
mbx envelope search -a work "invoice quarterly" --from cfo@company.com
```

### `mbx envelope thread <id>`

Return the thread containing the given envelope. Account is parsed from the ID. Uses server `THREAD` when available; falls back to mbx's ported algorithm over a bounded window. See [Q11 decision] / [ADR threading note in CONTEXT.md].

```bash
mbx envelope thread gmail:work:18f3c2a...
```

Output:
```json
{
  "v": 1,
  "data": {
    "thread_id": "gmail:work:18f3c1...",
    "envelopes": [ ...envelope objects in chronological order... ],
    "depth_map": { "<id>": 0, "<id2>": 1, ... }
  }
}
```

### `mbx envelope flag <id>...`

Add or remove flags on one or more envelopes.

```bash
mbx envelope flag gmail:work:18f3... --add seen
mbx envelope flag imap:work:INBOX:1:42 imap:work:INBOX:1:43 --add flagged --remove seen
```

| Flag | Description |
|---|---|
| `--add <flag>` | Repeatable. Vocabulary: `seen`, `flagged`, `answered`, `draft`, `deleted`. |
| `--remove <flag>` | Repeatable. Same vocabulary. |

Write-throughs to cache best-effort. See [ADR-0003](./adr/0003-cache-as-derived-state.md).

---

## `mbx message`

Expensive operations: full body fetch, compose, send.

### `mbx message read <id>...`

Read full message body.

```bash
mbx message read gmail:work:18f3... --preview
```

| Flag | Description |
|---|---|
| `--html` | Return raw HTML body instead of plain. |
| `--raw` | Return full MIME parts (replaces `body` field with `parts`). |
| `--preview` | Don't mark as seen. |
| `-H <name>` | Include the named header in output. Repeatable. |
| `--no-headers` | Omit the entire `headers` section. |

Default output:
```json
{
  "v": 1,
  "data": {
    "id": "gmail:work:18f3...",
    "thread_id": "gmail:work:18f3c1...",
    "from": "alice@x.com",
    "to": ["me@company.com"],
    "cc": [],
    "subject": "Q2 review",
    "date": "2026-05-14T09:33:00Z",
    "body": "Hi — wanted to share the deck...",
    "body_source": "text/plain",
    "attachments": [
      { "id": "gmail:work:18f3...:att-0", "filename": "deck.pdf", "size": 482311, "mime": "application/pdf" }
    ],
    "headers": { "Message-ID": "...", "In-Reply-To": "...", "References": "..." },
    "provider": "gmail"
  }
}
```

### `mbx message export <id>`

Dump raw RFC 5322 `.eml` to stdout. Useful for piping into other tools.

```bash
mbx message export gmail:work:18f3... > saved.eml
```

### `mbx message send`

Compose and send. Exactly one of `--body`, `--body-file`, `--body-stdin` must be provided. See [Q15 decision in CONTEXT.md].

```bash
mbx message send -a work \
  --to alex@company.com --cc lead@company.com \
  --subject "Status" --body-stdin --attach deck.pdf <<'EOF'
TL;DR up 4.3%. Full breakdown attached.
EOF
```

| Flag | Description |
|---|---|
| `--to <addr>` | Required. Repeatable. |
| `--cc <addr>` / `--bcc <addr>` | Repeatable. |
| `--subject <s>` | Required. |
| `--body <text>` | Inline body. Mutually exclusive with `--body-file` / `--body-stdin`. |
| `--body-file <path>` | Read body from file. |
| `--body-stdin` | Read body from stdin. |
| `--html` | Treat body as HTML (sets MIME `text/html`). |
| `--attach <path>` | Repeatable. |
| `--reply-to <addr>` | Override Reply-To. |
| `--draft` | Save as draft, don't send. |

Does not write-through to cache (only mutation of existing rows write-through).

### `mbx message reply <id>`

Reply to a message. Account, To, References, In-Reply-To are derived from the source message.

```bash
mbx message reply gmail:work:18f3... --body-stdin <<<"Acknowledged."
mbx message reply gmail:work:18f3... --all --body-stdin <<<"Team, ..."
```

Same body/attach flags as `send`. Plus:
| Flag | Description |
|---|---|
| `--all` | Reply to all recipients (To + Cc). |
| `--quote` | Include quoted original below body. |

### `mbx message forward <id>`

```bash
mbx message forward gmail:work:18f3... --to colleague@company.com --body-stdin <<<"FYI"
```

Same body/attach flags as `send`. Plus `--to` (required).

### `mbx message move <id>... <folder>`

```bash
mbx message move gmail:work:18f3... "Archive"
```

For Gmail, moves between labels (removes current `folders` set membership, adds target). For IMAP, IMAP MOVE or COPY+EXPUNGE fallback.

### `mbx message copy <id>... <folder>`

Same as `move` but doesn't remove from source.

### `mbx message delete <id>...`

Move to trash by default. `--permanent` skips trash.

---

## `mbx attachment`

### `mbx attachment list <message-id>`

List attachments on a message without downloading.

```bash
mbx attachment list gmail:work:18f3...
```

### `mbx attachment download <attachment-id>`

```bash
mbx attachment download gmail:work:18f3...:att-0 -o ~/Downloads/
mbx attachment download gmail:work:18f3...:att-0 -o ~/Downloads/q2-deck.pdf
```

| Flag | Description |
|---|---|
| `-o, --output <dir-or-path>` | Output destination. Defaults to config `attachment_dir`. |

---

## `mbx cache`

Opt-in per-account SQLite cache. Derived state — never authoritative. See [ADR-0003](./adr/0003-cache-as-derived-state.md).

Cache reads are scoped to `mbx cache *` verbs. Live verbs (`envelope list`, `message read`, ...) never read from cache.

### `mbx cache sync -a <acc>[,...]`

```bash
mbx cache sync -a work
mbx cache sync -a work --folder INBOX --days 90
mbx cache sync -a work --all                       # all folders
```

| Flag | Description |
|---|---|
| `--folder <name>` | Limit to one folder. Default: account's configured cache folders or INBOX. |
| `--days <n>` | Days to sync back. Default: account's configured `sync_days` or 30. |
| `--all` | Sync all folders. |

### `mbx cache status -a <acc>[,...]`

```json
{
  "v": 1,
  "data": {
    "account": "work",
    "path": "~/.cache/mbx/work.db",
    "size_bytes": 12483921,
    "rows": 4821,
    "last_sync_at": "2026-05-14T08:00:00Z",
    "drift_detected": 0,
    "folders": ["INBOX", "Sent"]
  }
}
```

### `mbx cache clear -a <acc>[,...]`

Delete the cache file for the account(s). `mbx cache sync` rebuilds from scratch.

### `mbx cache list -a <acc>[,...]`

Same flags and output shape as `envelope list`, but reads from cache only. Fast; bounded by what's been synced.

### `mbx cache search -a <acc>[,...] "<keywords>"`

Cache-side full-text search. Same flags as `envelope search`. Bounded by what's been synced.

---

## What's deliberately absent

- **No `template` noun.** Agents construct commands directly; no MML composition.
- **No top-level `flag` noun.** Folded into `envelope flag`.
- **No `--offline` flag on read verbs.** Cache reads live under the `cache` subtree only.
- **No interactive wizard / `$EDITOR` integration.** Anywhere.
- **No daemon / IMAP IDLE / `watch`.** mbx is one-shot per invocation.
- **No default-account fallback / `$MBX_ACCOUNT`.** `-a` is required when not implicit in an mbx ID.
- **No quoted-reply stripping default.** May add `--strip-quotes` later if there's demand.
