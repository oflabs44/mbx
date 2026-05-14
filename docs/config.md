# mbx config reference

Reference for mbx's config file. The config is parsed with `pelletier/go-toml/v2` ([ADR-0005](./adr/0005-toml-parser-pelletier-v2.md)) with `DisallowUnknownFields` enabled — typos in keys are surfaced with line/column on load (`config.invalid_toml`, exit 40).

## Where mbx looks

In order, first match wins:

1. `-c/--config <path>` — explicit file path passed on the command line.
2. `$MBX_CONFIG_DIR/config.toml` — opt-in override (tests, multi-config workflows).
3. `$XDG_CONFIG_HOME/mbx/config.toml` — the platform standard most CLI tools follow.
4. `$HOME/.config/mbx/config.toml` — universal fallback. mbx does not use `os.UserConfigDir` because on macOS it maps to `~/Library/Application Support`, which is wrong for CLI tooling.

This document is the authoritative schema. ADRs cover **why** the shape is what it is; this file covers **what** to put in the file.

## Top-level

```toml
# Optional. Path the cache subsystem treats as its root when an account opts in.
# Defaults to ~/.cache/mbx/.
cache-dir = "~/.cache/mbx"

# Optional. Directory `mbx attachment download` writes into when -o isn't passed.
attachment-dir = "~/Downloads"

# One [accounts.<name>] block per configured account. At least one required.
[accounts.<name>]
# ...
```

There is **no** `default-account`. `-a` is required unless the command's positional argument is an mbx ID. This is intentional ([CONTEXT.md "Flagged ambiguities"](../CONTEXT.md#flagged-ambiguities)).

## Account block

```toml
[accounts.<name>]
type  = "gmail" | "imap"          # required
email = "you@example.com"         # required, display purposes only
cache = { ... }                   # optional, see Cache

[accounts.<name>.backend]
# required for every account, shape depends on `type`

[accounts.<name>.send]
# required for IMAP accounts, omitted for Gmail
```

`<name>` is the account ID used everywhere — `-a <name>` on the CLI, the second segment of every mbx ID. Choose short, stable identifiers (`work`, `gmail-personal`, `getu`). Renaming an account invalidates all mbx IDs that reference it.

## Backend — Gmail

Gmail accounts always use OAuth 2.0 with XOAUTH2.

```toml
[accounts.gmail-personal]
type  = "gmail"
email = "you@gmail.com"

[accounts.gmail-personal.backend.auth]
type      = "oauth2"
client-id = "1234.apps.googleusercontent.com"
auth-url  = "https://accounts.google.com/o/oauth2/v2/auth"
token-url = "https://www.googleapis.com/oauth2/v3/token"
# Optional. Defaults to localhost:0 (mbx picks an unused port).
redirect-host = "localhost"
redirect-port = 0

[accounts.gmail-personal.backend.auth.client-secret]
# Secret block — see "Secrets" below
cmd = "op read op://Dev/mbx-gmail-personal/client-secret"

[accounts.gmail-personal.backend.auth.refresh-token]
# Secret block + write_cmd (mandatory for OAuth refresh-token)
cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"
write_cmd = "op item edit mbx-gmail-personal refresh-token[password]=-"
```

Send is handled by the Gmail API; no separate `[send]` block.

## Backend — IMAP

IMAP accounts use IMAP for reads and a separate SMTP `[send]` block for writes.

```toml
[accounts.work]
type  = "imap"
email = "you@company.com"

[accounts.work.backend]
host = "imap.company.com"
port = 993
tls  = "tls"                       # "tls" | "starttls" | "none"

[accounts.work.backend.auth]
type     = "password"              # "password" | "oauth2"
username = "you@company.com"
# Secret variant inlined on the auth block (one of raw | keyring | cmd):
cmd      = "op read op://Dev/mbx-work/password"

[accounts.work.send]
host = "smtp.company.com"
port = 587
tls  = "starttls"

# Send auth defaults to inheriting from backend.auth unless overridden:
# [accounts.work.send.auth]
# type     = "password"
# username = "..."
# cmd      = "..."
```

For Proton accounts, run the local Proton Bridge and point both `backend` and `send` at `127.0.0.1` on the bridge's configured ports. Auth is password (the bridge-issued one), not OAuth.

For IMAP+OAuth (corporate Microsoft 365, Google Workspace IMAP), the `auth.type` is `"oauth2"` with the same nested secret blocks as Gmail.

## Secrets

Every confidential value (password, OAuth client secret, refresh token) is supplied via a **secret block** with exactly one of three variants ([ADR-0001](./adr/0001-secrets-resolution-model.md)):

```toml
# raw — inline string. Testing only; never check in.
raw = "the-actual-secret"

# keyring — OS keychain entry. The string is the keyring item key.
keyring = "mbx-gmail-personal-refresh-token"

# cmd — any shell command. Its stdout is the secret. Trailing newlines trimmed.
cmd = "op read op://Dev/mbx-gmail-personal/client-secret"
```

Adding more than one variant in the same block is a config error.

### `write_cmd` (rotated secrets)

For OAuth `refresh-token` blocks **only**, you must also set `write_cmd`. mbx pipes the new value to its stdin on rotation. `mbx account auth` refuses to run without it (`auth.missing_write_cmd`, exit 11).

```toml
[accounts.gmail-personal.backend.auth.refresh-token]
cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"
write_cmd = "op item edit mbx-gmail-personal refresh-token[password]=-"
```

Read variant and write target are decoupled on purpose ([ADR-0001](./adr/0001-secrets-resolution-model.md#write-target-is-explicit-not-inferred-from-read)). If you use `cmd` to read but want to write to keyring, that's fine — declare both.

### Recipe — 1Password

```toml
[accounts.gmail-personal.backend.auth.client-secret]
cmd = "op read op://Dev/mbx-gmail-personal/client-secret"

[accounts.gmail-personal.backend.auth.refresh-token]
cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"
write_cmd = "op item edit mbx-gmail-personal refresh-token[password]=-"
```

### Recipe — `pass`

```toml
[accounts.gmail-personal.backend.auth.refresh-token]
cmd       = "pass mbx-gmail-personal/refresh-token"
write_cmd = "pass insert -m -f mbx-gmail-personal/refresh-token"
```

### Recipe — macOS keychain via `security`

```toml
[accounts.gmail-personal.backend.auth.refresh-token]
cmd       = "security find-generic-password -a $USER -s mbx-gmail-personal-rt -w"
write_cmd = "security add-generic-password -U -a $USER -s mbx-gmail-personal-rt -w"
```

### Recipe — `keyring` variant (OS keychain native)

```toml
[accounts.gmail-personal.backend.auth.refresh-token]
keyring   = "mbx-gmail-personal-refresh-token"
write_cmd = "security add-generic-password -U -a $USER -s mbx-gmail-personal-refresh-token -w"
```

## Cache (optional)

Per-account, opt-in SQLite cache. Off by default ([ADR-0003](./adr/0003-cache-as-derived-state.md)).

```toml
[accounts.work.cache]
path       = "~/.cache/mbx/work.db"        # default if omitted: <cache-dir>/<name>.db
sync_days  = 30                             # default: 30
folders    = ["INBOX", "Sent", "Archive"]   # default: ["INBOX"]
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
cache-dir      = "~/.cache/mbx"
attachment-dir = "~/Downloads"

# Gmail (OAuth, secrets in 1Password)
[accounts.gmail-personal]
type  = "gmail"
email = "you@gmail.com"

[accounts.gmail-personal.backend.auth]
type      = "oauth2"
client-id = "1234.apps.googleusercontent.com"
auth-url  = "https://accounts.google.com/o/oauth2/v2/auth"
token-url = "https://www.googleapis.com/oauth2/v3/token"

[accounts.gmail-personal.backend.auth.client-secret]
cmd = "op read op://Dev/mbx-gmail-personal/client-secret"

[accounts.gmail-personal.backend.auth.refresh-token]
cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"
write_cmd = "op item edit mbx-gmail-personal refresh-token[password]=-"

# IMAP corporate account (password auth, cache enabled)
[accounts.work]
type  = "imap"
email = "you@company.com"

[accounts.work.cache]
sync_days = 90
folders   = ["INBOX", "Sent"]

[accounts.work.backend]
host = "imap.company.com"
port = 993
tls  = "tls"

[accounts.work.backend.auth]
type     = "password"
username = "you@company.com"
cmd      = "op read op://Dev/mbx-work/password"

[accounts.work.send]
host = "smtp.company.com"
port = 587
tls  = "starttls"
```

## What's deliberately absent

- **No `default-account` key.** `-a` is always required when not implicit in an mbx ID.
- **No env-var fallback.** `$MBX_ACCOUNT` is not consulted.
- **No inline private keys / cert paths in v1.** `cmd` covers any custom resolver.
- **No top-level `secrets.<name>` block referenced by indirection.** Secrets live inline next to the auth they belong to; less indirection beats less duplication for a small config.
- **No keyring as a built-in OAuth write target.** Use `write_cmd` with `security` / `secret-tool` / equivalent ([ADR-0001](./adr/0001-secrets-resolution-model.md)).
