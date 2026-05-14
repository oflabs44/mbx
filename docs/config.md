# mbx config reference

Reference for mbx's config file. The config is parsed with `pelletier/go-toml/v2` ([ADR-0005](./adr/0005-toml-parser-pelletier-v2.md)) with `DisallowUnknownFields` enabled — typos in keys are surfaced with line/column on load (`config.invalid`, exit 40).

The TOML shape is the himalaya-derived dotted-key form with mbx extensions ([ADR-0006](./adr/0006-himalaya-config-shape.md)). Every account is one `[accounts.<name>]` block; everything inside an account uses dotted keys.

## Where mbx looks

In order, first match wins:

1. `-c/--config <path>` — explicit file path passed on the command line.
2. `$MBX_CONFIG_DIR/config.toml` — opt-in override (tests, multi-config workflows).
3. `$XDG_CONFIG_HOME/mbx/config.toml` — the platform standard most CLI tools follow.
4. `$HOME/.config/mbx/config.toml` — universal fallback. mbx does not use `os.UserConfigDir` because on macOS it maps to `~/Library/Application Support`, which is wrong for CLI tooling.

This document is the authoritative schema. ADRs cover **why** the shape is what it is; this file covers **what** to put in the file.

## Top-level (global defaults)

```toml
# Optional. Directory mbx writes downloads into when `mbx attachment download
# --output` is not passed. Defaults to the system temp directory.
downloads-dir = "~/Downloads"

# Optional. Root directory for opt-in per-account caches. Defaults to
# ~/.cache/mbx. Per-account `cache.path` takes precedence when set.
cache-dir = "~/.cache/mbx"
```

There is **no** `default-account`. `-a` is required unless the command's positional argument is an mbx ID ([CONTEXT.md "Flagged ambiguities"](../CONTEXT.md#flagged-ambiguities)).

## Account block

```toml
[accounts.<name>]
email = "you@example.com"          # required, display purposes only

backend.type = "imap" | "gmail"    # required
# ... see "Backend — Gmail" or "Backend — IMAP" below
```

`<name>` is the account ID used everywhere — `-a <name>` on the CLI, the second segment of every mbx ID. Choose short, stable identifiers (`work`, `gmail-personal`, `getu`). Renaming an account invalidates all mbx IDs that reference it.

## Backend — Gmail

Gmail accounts use the Gmail HTTP API for both read and send. There is no `message.send.backend` block (the API handles send), and `backend.host` / `backend.port` / `backend.encryption` are forbidden — the validator rejects them.

```toml
[accounts.gmail-personal]
email = "you@gmail.com"

backend.type  = "gmail"
backend.login = "you@gmail.com"

backend.auth.type      = "oauth2"
backend.auth.client-id = "1234.apps.googleusercontent.com"
backend.auth.auth-url  = "https://accounts.google.com/o/oauth2/v2/auth"
backend.auth.token-url = "https://www.googleapis.com/oauth2/v3/token"
backend.auth.method    = "xoauth2"
backend.auth.scopes    = ["https://mail.google.com/"]
# backend.auth.pkce          = true
# backend.auth.redirect-host = "localhost"
# backend.auth.redirect-port = 0

# Secret blocks (see "Secrets" below).
backend.auth.client-secret.cmd = "op read op://Dev/mbx-gmail-personal/client-secret"

backend.auth.refresh-token.cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"
backend.auth.refresh-token.write_cmd = 'op item edit mbx-gmail-personal "refresh-token=$(cat)" </dev/null'
```

## Backend — IMAP

IMAP accounts use IMAP for reads and a parallel `message.send.backend` (SMTP) for writes.

```toml
[accounts.work]
email = "you@company.com"

backend.type            = "imap"
backend.host            = "imap.company.com"
backend.port            = 993
backend.encryption.type = "tls"          # "tls" | "start-tls" | "none"
backend.login           = "you@company.com"
backend.auth.type       = "password"
backend.auth.cmd        = "op read op://Dev/mbx-work/imap-password"

message.send.backend.type            = "smtp"
message.send.backend.host            = "smtp.company.com"
message.send.backend.port            = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login           = "you@company.com"
message.send.backend.auth.type       = "password"
message.send.backend.auth.cmd        = "op read op://Dev/mbx-work/smtp-password"
```

`message.send.backend.auth.*` has no implicit inheritance from `backend.auth.*` — configure it explicitly. The two are often identical (Proton bridge, corporate IMAP+SMTP with shared password) but mbx never infers.

For **Proton accounts**, run the local Proton Bridge and point both backends at `127.0.0.1` on the bridge's configured ports. Auth is password (the bridge-issued one), not OAuth. The bridge ships a self-signed (and on macOS, non-strict-compliant) TLS certificate, so on a typical desktop install you'll also need:

```toml
backend.encryption.insecure              = true
message.send.backend.encryption.insecure = true
```

`encryption.insecure` disables TLS certificate verification on that backend. **Only use it for loopback relays** like Proton Bridge — on a remote host it turns the connection into a downgrade target. mbx defaults to verification on; this is opt-in per-backend.

For **IMAP+OAuth** (corporate Microsoft 365, Google Workspace IMAP), `backend.auth.type = "oauth2"` with the same nested secret blocks as Gmail above. `message.send.backend.auth.*` may use OAuth too.

### `backend.thread_window` (IMAP only)

Caps the corpus the client-side threading algorithm scans when the server doesn't advertise `THREAD=REFERENCES`. Default `1000`; the algorithm fetches the most-recent N envelopes in the anchor's folder and threads over them. Servers that do advertise THREAD ignore this knob — the server identifies the cluster.

```toml
backend.thread_window = 1000   # default; bump if a folder routinely holds long threads beyond the recent 1000
```

## Folder aliases

mbx commands refer to folders by canonical roles (`inbox`, `sent`, `drafts`, `trash`) but each provider names its folders differently. `folder.aliases.*` is the mapping.

```toml
folder.aliases.inbox  = "INBOX"
folder.aliases.sent   = "Sent"
folder.aliases.drafts = "Drafts"
folder.aliases.trash  = "Trash"

# Extra user aliases are accepted:
folder.aliases.archive2024 = "Archives/2024"
```

- `folder.aliases.inbox` is **required** for `imap` backends.
- For `gmail` backends, folder.aliases is optional — mbx defaults map `inbox`→`INBOX`, `sent`→`[Gmail]/Sent Mail`, `drafts`→`[Gmail]/Drafts`, `trash`→`[Gmail]/Trash`.
- Aliases consumed by mbx today: `inbox` (envelope-list default). The rest are accepted now and consumed as Phase 4 (`message.send.save-copy`) and Phase 5 (threading) land.

## Secrets

Every confidential value (password, OAuth client secret, refresh token) is supplied via a **secret block** with exactly one of three variants ([ADR-0001](./adr/0001-secrets-resolution-model.md)):

```toml
# raw — inline string. Testing only; never check in.
backend.auth.raw = "the-actual-secret"

# keyring — OS keychain entry. The string is the keyring item key.
backend.auth.keyring = "mbx-gmail-personal-refresh-token"

# cmd — any shell command. Its stdout is the secret. Trailing newlines trimmed.
backend.auth.cmd = "op read op://Dev/mbx-gmail-personal/client-secret"
```

For password auth, the variant is set directly on `backend.auth.*` (one of `raw` / `keyring` / `cmd`). For OAuth, each rotated secret has its own nested block (`backend.auth.client-secret.*`, `backend.auth.refresh-token.*`, `backend.auth.access-token.*`), each carrying its own variant key plus optionally `write_cmd`.

Setting more than one variant in the same block is a config error.

### `write_cmd` (rotated secrets)

For OAuth `refresh-token` blocks **only**, you must also set `write_cmd`. mbx pipes the new value to its stdin on rotation. `mbx account auth` refuses to run without it (`auth.missing_write_cmd`, exit 11).

```toml
backend.auth.refresh-token.cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"
backend.auth.refresh-token.write_cmd = 'op item edit mbx-gmail-personal "refresh-token=$(cat)" </dev/null'
```

Read variant and write target are decoupled on purpose ([ADR-0001](./adr/0001-secrets-resolution-model.md#write-target-is-explicit-not-inferred-from-read)). If you use `cmd` to read but want to write to keyring, that's fine — declare both.

### Recipe — 1Password

```toml
backend.auth.client-secret.cmd = "op read op://Dev/mbx-gmail-personal/client-secret"

backend.auth.refresh-token.cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"

# write_cmd receives the new refresh token on stdin. Splice it into the
# assignment with $(cat), then redirect op's stdin to /dev/null so op doesn't
# try to parse it as a JSON edit-body (op v2 does that when stdin is non-TTY
# and the result is "[ERROR] ... invalid JSON provided"). Use a TOML literal
# string (single-quoted) so the $ and " survive into the shell verbatim.
#
# The field name in the assignment ("refresh-token" here) must already exist
# on the 1Password item. Drop any `[type]` annotation — that triggers the
# JSON-body path in current op versions, even with </dev/null.
backend.auth.refresh-token.write_cmd = 'op item edit mbx-gmail-personal "refresh-token=$(cat)" </dev/null'
```

The `mbx-gmail-personal` item must already have a field literally named `refresh-token` of type `password`. If you'd rather use a Login item's built-in `password` field, point both `cmd` and `write_cmd` at it:

```toml
backend.auth.refresh-token.cmd       = "op read op://Dev/mbx-gmail-personal/password"
backend.auth.refresh-token.write_cmd = 'op item edit mbx-gmail-personal "password=$(cat)" </dev/null'
```

### Argv visibility

After shell expansion, the refresh token sits in the assignment argument that becomes part of `op`'s argv — visible to `ps -ef` while the command runs. On single-user macOS this is generally acceptable; on a shared host it isn't.

For sensitive deployments, prefer a write_cmd that pipes the value to `op` via stdin (e.g., a JSON edit body to `op item edit --in-file -`) so the token never lands in argv. Build that with `jq` or a small helper script. mbx's `account auth` preflight verifies whichever shape you pick.

### Recipe — `pass`

```toml
backend.auth.refresh-token.cmd       = "pass mbx-gmail-personal/refresh-token"
backend.auth.refresh-token.write_cmd = "pass insert -m -f mbx-gmail-personal/refresh-token"
```

`pass insert -m` reads the value from stdin as-is; no shell tricks needed.

### Recipe — macOS keychain via `security`

```toml
backend.auth.refresh-token.cmd = "security find-generic-password -a $USER -s mbx-gmail-personal-rt -w"

# `security -w` requires the value as an argument, not stdin. Splice with $(cat)
# in a TOML literal string.
backend.auth.refresh-token.write_cmd = 'security add-generic-password -U -a "$USER" -s mbx-gmail-personal-rt -w "$(cat)"'
```

### Recipe — `keyring` variant (OS keychain native)

```toml
backend.auth.refresh-token.keyring   = "mbx-gmail-personal-refresh-token"
backend.auth.refresh-token.write_cmd = 'security add-generic-password -U -a "$USER" -s mbx-gmail-personal-refresh-token -w "$(cat)"'
```

## Cache (optional)

Per-account, opt-in SQLite cache. Off by default ([ADR-0003](./adr/0003-cache-as-derived-state.md)).

```toml
cache.path      = "~/.cache/mbx/work.db"   # default if omitted: <cache-dir>/<name>.db
cache.sync_days = 30                        # default: 30
cache.folders   = ["INBOX", "Sent"]         # default: resolved alias for `inbox`
```

Live verbs never read this cache; it's only touched by `mbx cache *` subcommands and best-effort write-through from mutating writes.

## Validation

After editing the file, validate per account:

```bash
mbx account doctor <name>
```

Checks TOML parse, all `cmd` secrets resolve, OAuth refresh / IMAP LOGIN succeeds, folder list works, and capability probing (THREAD, IDLE, ...). Output shape is in [commands.md](./commands.md#mbx-account-doctor-name).

## Worked example — two accounts

```toml
downloads-dir = "~/Downloads"
cache-dir     = "~/.cache/mbx"

# Gmail (OAuth, secrets in 1Password)
[accounts.gmail-personal]
email = "you@gmail.com"

backend.type  = "gmail"
backend.login = "you@gmail.com"

backend.auth.type      = "oauth2"
backend.auth.client-id = "1234.apps.googleusercontent.com"
backend.auth.auth-url  = "https://accounts.google.com/o/oauth2/v2/auth"
backend.auth.token-url = "https://www.googleapis.com/oauth2/v3/token"
backend.auth.method    = "xoauth2"
backend.auth.scopes    = ["https://mail.google.com/"]

backend.auth.client-secret.cmd = "op read op://Dev/mbx-gmail-personal/client-secret"

backend.auth.refresh-token.cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"
backend.auth.refresh-token.write_cmd = 'op item edit mbx-gmail-personal "refresh-token=$(cat)" </dev/null'

# IMAP corporate account (password auth, cache enabled)
[accounts.work]
email = "you@company.com"

backend.type            = "imap"
backend.host            = "imap.company.com"
backend.port            = 993
backend.encryption.type = "tls"
backend.login           = "you@company.com"
backend.auth.type       = "password"
backend.auth.cmd        = "op read op://Dev/mbx-work/imap-password"

message.send.backend.type            = "smtp"
message.send.backend.host            = "smtp.company.com"
message.send.backend.port            = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login           = "you@company.com"
message.send.backend.auth.type       = "password"
message.send.backend.auth.cmd        = "op read op://Dev/mbx-work/smtp-password"

folder.aliases.inbox  = "INBOX"
folder.aliases.sent   = "Sent"
folder.aliases.drafts = "Drafts"
folder.aliases.trash  = "Trash"

cache.sync_days = 90
cache.folders   = ["INBOX", "Sent"]
```

## What's deliberately absent

- **No `default = true` per account.** `-a` is always required when not implicit in an mbx ID ([CONTEXT.md](../CONTEXT.md)).
- **No env-var fallback for account selection.** `$MBX_ACCOUNT` is not consulted.
- **No `message.send.auth` inheritance.** Send-side auth is always explicit.
- **No `template.{new,reply,forward}.*`** (editor-driven compose templates). mbx is one-shot per invocation; `--body` / `--body-file` / `--body-stdin` cover composition.
- **No `message.read.*` rendering / format knobs.** mbx output is JSON-shaped ([ADR-0004](./adr/0004-json-output-contract.md)); rendering is the caller's concern.
- **No `pgp.*` support in v1.** Out of scope.
- **No `account.list.table.*` or other TUI styling.** JSON-only output; tables are rendered client-side.
- **No `*.list.page-size` keys.** Pagination is cursor-based ([commands.md](./commands.md)).
- **No `backend.type = "maildir" | "notmuch" | "sendmail"`.** Out of mbx scope.
- **No keyring as a built-in OAuth write target.** Use `write_cmd` with `security` / `secret-tool` / equivalent ([ADR-0001](./adr/0001-secrets-resolution-model.md)).
- **No inline private keys / cert paths in v1.** `cmd` covers any custom resolver.
- **No top-level `secrets.<name>` block referenced by indirection.** Secrets live inline next to the auth they belong to; less indirection beats less duplication for a small config.
