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
| `20–29` | Provider (`20` rate limited, `21` not found, `22` ID invalidated, `23` network timeout, `24` capability unsupported, ...) |
| `30–39` | Cache (`30` unavailable, `31` schema mismatch, ...) |
| `40–49` | Config (`40` invalid TOML, `41` unknown account, ...) |
| `50–59` | Fanout (`50` all-accounts-failed, ...) |

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

Validate config and probe the account. Checks: TOML parse, all `cmd` secrets resolve, OAuth refresh or IMAP LOGIN succeeds, folder list works, server capabilities (THREAD, IDLE, ...).

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

### `mbx account rename <old> <new>`

Rename an account. Rewrites `[accounts.<old>]` (and any `[accounts.<old>.*]` sub-sections) to `<new>`, and inserts `aliases = ["<old>"]` so previously-emitted mbx IDs continue to resolve via [ADR-0007](./adr/0007-account-renames-via-aliases.md). Atomic; does not touch external secret stores.

```bash
mbx account rename personal personal-gmail
```

| Error | Code | Exit | Cause |
|---|---|---|---|
| target name already used | `config.invalid` | 40 | `[accounts.<new>]` already in the file |
| source absent | `config.unknown_account` | 41 | `[accounts.<old>]` not in the file |
| account already has an `aliases` list | `config.invalid` | 40 | Merge by hand then re-run rename |

After rename, the old name resolves through the alias (`-a personal` still works), but mbx **stamps emitted IDs with the canonical new name**. Skills that hold old IDs continue to work; new IDs returned from any verb carry `<new>` going forward.

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

Create a folder. On Gmail, creates a user-visible label.

```bash
mbx folder add -a work "Project Alpha"
```

### `mbx folder delete -a <acc> <name>`

Delete a folder. On most IMAP servers, DELETE refuses non-empty mailboxes; `--force` first purges then deletes. On Gmail, labels are not message containers, so delete only removes the label from every message it carried (the messages stay); `--force` is accepted but has no effect.

| Flag | Description |
|---|---|
| `--force` | IMAP: purge first if non-empty. Gmail: no-op. |

```bash
mbx folder delete -a work "Project Alpha"
mbx folder delete -a work "Old Stuff" --force
```

### `mbx folder expunge -a <acc> <name>`

Permanently remove messages already flagged `\Deleted` from the folder. IMAP-specific: SELECT + EXPUNGE. On Gmail this is a no-op (server-side Trash auto-purges after 30 days) — the verb succeeds without doing anything, so cross-provider scripts can run it unconditionally.

```bash
mbx folder expunge -a work INBOX
```

### `mbx folder purge -a <acc> <name>`

Delete **every** message in the folder. Irreversible; requires `--yes`.

| Flag | Description |
|---|---|
| `--yes` | Required confirmation. Missing → `input.missing_flag` (exit 2). |

- IMAP: SELECT, `STORE 1:* +FLAGS.SILENT \Deleted`, `EXPUNGE`. The folder itself remains. If STORE succeeds but EXPUNGE fails, messages stay `\Deleted`-flagged — re-run purge (or `expunge`) to finish.
- Gmail: list every message carrying the label, then `users.messages.delete` each (hard-delete, no Trash hop). **Surprise worth knowing**: Gmail messages routinely carry multiple labels. Purging a label hard-deletes every message *also* carrying it, regardless of any other folder/label membership — purging "Receipts" wipes Receipts-tagged messages even if they're also in INBOX. Almost never what you want for non-leaf labels.

```bash
mbx folder purge -a work "Project Alpha" --yes
```

**Success shape** (all four verbs):
```json
{
  "v": 1,
  "data": { "name": "Project Alpha", "op": "purge" },
  "meta": { "accounts_queried": ["work"] }
}
```

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
| `--limit <n>` | **Per-account** limit (not total). Default 20. With `-a a,b,c` you'll get up to `3 × n` envelopes. |
| `--unread` / `--starred` / `--has-attachment` | Boolean filters. |
| `--from <addr>` / `--to <addr>` | Address filters. |
| `--after <date>` / `--before <date>` | ISO-8601 dates. |
| `--query "<raw>"` | Provider-native raw query (Gmail query syntax / IMAP SEARCH). Escape hatch. |
| `--cursor <c>` | Resume from a previous response's `meta.next_cursors[<account>]`. Rejected with `usage.invalid` (exit 2) when paired with multi-account `-a` — page accounts individually. |
| `--strict` | Fail entire command if any fanout account fails. Without it, partial success: any account that succeeded contributes envelopes, failures land in `meta.errors` keyed by account, exit `0`. |

**Multi-account behaviour.** When `-a` lists more than one account, mbx fans out: each account is dispatched concurrently and its envelopes are merged into one response sorted by `date` descending. Per-account state (cursors, errors) lives under `meta.next_cursors` and `meta.errors`, keyed by the canonical account name ([ADR-0007](./adr/0007-account-renames-via-aliases.md)) — aliases are resolved before dispatch. Wildcards in `-a` (e.g. `*`) are rejected with `usage.invalid`.

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
      "flags": [],
      "folders": ["INBOX"],
      "snippet": "Hi — wanted to share the deck...",
      "has_attachment": false,
      "provider": "gmail",
      "gmail": { "labels": ["INBOX","UNREAD","Label_42"] }
    }
  ],
  "meta": {
    "accounts_queried": ["work", "gmail-personal"],
    "next_cursors": { "work": "eyJwYWdlVG9rZW4..." },
    "errors": {
      "gmail-personal": {
        "code": "provider.network_timeout",
        "message": "Gmail upstream error (503): backend unavailable"
      }
    }
  }
}
```

`meta.errors` is omitted when every account succeeded. With `--strict`, the first account failure aborts the command and the top-level error envelope (stderr) is the failing account's error verbatim.

### `mbx envelope search`

Cross-folder keyword search. Same flags as `list`, plus a positional `"<keywords>"`. Fanout, partial-success, and `--strict` semantics are identical to `list`.

```bash
mbx envelope search -a work "invoice quarterly" --from cfo@company.com
mbx envelope search -a work,gmail-personal "shipping notification"        # fanout
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

Add or remove flags on one or more envelopes. All IDs must share an account.

```bash
mbx envelope flag gmail:work:18f3... --add seen
mbx envelope flag imap:work:INBOX:1:42 imap:work:INBOX:1:43 --add flagged --remove seen
```

| Flag | Description |
|---|---|
| `--add <flag>[,<flag>...]` | Repeatable or comma-separated. Vocabulary: `seen`, `flagged`, `answered`, `draft`, `deleted`. |
| `--remove <flag>[,<flag>...]` | Repeatable or comma-separated. Same vocabulary. |

At least one of `--add` / `--remove` must be non-empty.

Multi-ID input is **fail-fast**: the first ID (Gmail) or folder group (IMAP) that fails aborts the rest. Earlier mutations are already applied server-side. The supported flag diffs are idempotent, so retrying the same command after a transient failure is safe.

**Provider support:**
- IMAP: full vocabulary (`\Seen`, `\Flagged`, `\Answered`, `\Draft`, `\Deleted`).
- Gmail: only `seen` and `flagged` (maps to `UNREAD`-inverse and `STARRED` labels). `answered` / `draft` / `deleted` return `provider.unsupported` (exit 24) — use `message delete` for trash/delete semantics.

Success shape:
```json
{
  "v": 1,
  "data": {
    "ids": ["imap:work:INBOX:1:42", "imap:work:INBOX:1:43"],
    "flags_added": ["flagged"],
    "flags_removed": ["seen"]
  },
  "meta": { "accounts_queried": ["work"] }
}
```

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

**From address:** taken from `accounts.<name>.email`. Required at config-load time.

**Body input rule:** exactly one of `--body` / `--body-file` / `--body-stdin`. Missing → `input.missing_flag` (exit 2); two or more → `input.ambiguous_body` (exit 2).

**Provider routing:**
- Gmail accounts: `users.messages.send` over the Gmail HTTP API (the `message.send.backend` block is forbidden for Gmail).
- IMAP accounts: SMTP via `message.send.backend.*` (host/port/encryption/login/auth). Auth is NOT inherited from `backend.auth.*` — configure explicitly.

Success shape:
```json
{
  "v": 1,
  "data": {
    "from": "you@company.com",
    "to": ["alex@company.com"],
    "cc": ["lead@company.com"],
    "subject": "Status"
  },
  "meta": { "accounts_queried": ["work"] }
}
```

Bcc never appears in the body delivered to recipients. On SMTP, the addresses are sent via RCPT TO only; on Gmail, the API receives a `Bcc:` header (so it knows to deliver) and strips it before delivery.

Does not write-through to cache (only mutation of existing rows write-through).

### `mbx message reply <id>`

Reply to a message. Account, To, References, In-Reply-To, and Subject are derived from the source.

```bash
mbx message reply gmail:work:18f3... --body "Acknowledged."
mbx message reply gmail:work:18f3... --all --quote --body-stdin <<<"Team, ..."
```

| Flag | Description |
|---|---|
| `--all` | Reply to all: To = source.From; Cc = source.To + source.Cc minus the replying account's own address. |
| `--quote` | Append the source body quoted (`> ` prefix) below the new body, with an attribution line. |
| `--body` / `--body-file` / `--body-stdin` | Exactly one required. Same rules as `send`. |
| `--html` | Treat body as HTML. |
| `--attach <path>` | Repeatable. |
| `--reply-to <addr>` | Override Reply-To. |

**Derived headers:** `In-Reply-To = source.Message-ID`; `References = source.References + source.Message-ID` (or just `Message-ID` when the source has no References).

**Subject:** `Re: <original>` unless the source already starts with `Re:` (case-insensitive — avoids "Re: Re:" stacking).

Success shape mirrors `send`.

### `mbx message forward <id>`

Forward a message. The original is always quoted below the new body (the point of forwarding).

```bash
mbx message forward gmail:work:18f3... --to colleague@company.com --body "FYI"
mbx message forward imap:work:INBOX:1:42 --to a@x --cc b@y --body-stdin <<<"see below"
```

| Flag | Description |
|---|---|
| `--to <addr>` | Required. Repeatable. |
| `--cc <addr>` / `--bcc <addr>` | Repeatable. |
| `--body` / `--body-file` / `--body-stdin` | Exactly one required. |
| `--html` | Treat body as HTML. |
| `--attach <path>` | Repeatable. New attachments only — the source's attachments are not re-attached automatically. |
| `--reply-to <addr>` | Override Reply-To. |

**Subject:** `Fwd: <original>` unless already prefixed with `Fwd:` or `Fw:` (case-insensitive).

**Threading:** forwards do NOT carry `In-Reply-To` / `References` — the new recipients aren't part of the source thread.

Success shape mirrors `send`.

### `mbx message move <id>... <folder>`

Move one or more messages to a destination folder. All IDs must share an account.

```bash
mbx message move gmail:work:18f3... "Archive"
mbx message move imap:work:INBOX:1:42 imap:work:INBOX:1:43 "Archive"
```

**Provider behaviour:**
- Gmail: adds the dest label and removes `INBOX`. The mbx ID is unchanged (Gmail messages are stable across label changes). If the source isn't in `INBOX`, the remove is a server-side no-op.
- IMAP: uses the server's `MOVE` extension when present; falls back to `COPY`+`STORE`+`EXPUNGE` automatically. New IDs are emitted from the server's `COPYUID` response (requires `UIDPLUS` or IMAP4rev2); on servers without that, `new_ids` is empty and the caller must re-list to address the messages.

Success shape:
```json
{
  "v": 1,
  "data": {
    "ids": ["imap:work:INBOX:1:42"],
    "new_ids": ["imap:work:Archive:7:91"],
    "dest": "Archive"
  },
  "meta": { "accounts_queried": ["work"] }
}
```

Multi-ID input is **fail-fast** across folders. `MOVE` is **not** idempotent (re-running after partial failure will report "message not found" on the IDs already moved); re-list to recover. `COPY` is idempotent in the sense that copies accumulate, not that the operation is no-op on retry.

### `mbx message copy <id>... <folder>`

Same flag and output shape as `move`, but the source UIDs remain valid.

For Gmail, copy adds the dest label without removing any. The "copy" still refers to the same single Gmail message; the returned `new_ids` mirrors the input.

### `mbx message delete <id>...`

Move to trash by default; `--permanent` hard-deletes.

```bash
mbx message delete gmail:work:18f3...                       # → Trash label
mbx message delete imap:work:INBOX:1:42 --permanent          # STORE \Deleted + EXPUNGE
```

| Flag | Description |
|---|---|
| `--permanent` | Skip trash; hard-delete. Irreversible. |

**Provider behaviour:**
- Gmail: default → `users.messages.trash` (recoverable); `--permanent` → `users.messages.delete`.
- IMAP: default → `MOVE` to `folder.aliases.trash` (must be configured per account; `config.invalid` / exit 40 if unset). `--permanent` → `STORE +FLAGS.SILENT \Deleted` then `UID EXPUNGE` (or plain `EXPUNGE` on servers without `UIDPLUS`).

**Retry semantics on partial failure (multi-ID):**
- Default delete (trash, both providers) is idempotent: re-running over the same ids is safe.
- `--permanent` Gmail: NOT safe to retry — already-deleted ids return 404. Re-list before retrying.
- `--permanent` IMAP: if `STORE \Deleted` succeeds but `EXPUNGE` fails, messages remain `\Deleted`-flagged but visible in the source folder. Re-running `--permanent` completes the operation.

Success shape mirrors `move`/`copy` but omits `new_ids` and `dest`.

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
