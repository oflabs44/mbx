# 0006 — Adopt himalaya's TOML config shape, with mbx-specific extensions

mbx adopts himalaya's flat-dotted-keys TOML config shape as its config format, including himalaya's hierarchy (`backend.*`, `message.send.backend.*`, `folder.aliases.*`, `message.{read,write,send,delete}.*`). The Go struct schema is reshaped to mirror this. Existing mbx-specific decisions — `write_cmd` on rotated secrets, Gmail-API as a real backend, opt-in SQLite `cache`, JSON-only output — are kept as extensions on top of the himalaya base.

This is a one-time alignment done **before** Phase 1's `account auth` / `account doctor` / `account remove` handlers land, so those handlers and all of Phase 2+ build against the final shape.

## Why adopt himalaya's shape

1. **AGENTS.md §11 already commits us to lifting it.** "Lift: TOML config shape (`backend.auth.*` and per-secret `raw | keyring | cmd` variants)" — we'd been lifting *partially*, with our own header-per-level style. The full shape is more consistent.
2. **Dotted-keys give one block per account.** Copy-pasting accounts between configs, commenting/uncommenting accounts in `account add` / `account remove`, and visual scanning all win.
3. **`message.{read,write,send,delete,*}` namespace is load-bearing for Phase 4+ message-policy knobs** mbx will need (`message.send.save-copy`, `message.send.pre-hook`, `message.delete.style`). Without a namespace, those land as top-level keys with no structural home.
4. **`folder.aliases.{inbox,sent,drafts,trash,*}` is the right place for the inbox-vs-INBOX problem.** mbx normalizes via the **Folder** vocab in CONTEXT.md, but provider folder names still vary; aliases resolve that mismatch without polluting the domain.
5. **The type discriminator moves from the account level to the backend level.** Currently the value lives at `[accounts.<name>].type = "gmail" | "imap"` — one level up from every field it controls (`backend.host`, `backend.port`, `backend.auth.*`, etc.). Himalaya puts the discriminator at `backend.type`, sitting next to the fields it discriminates. We adopt that. As a side effect, the value set is mbx-specific (`imap | gmail`) rather than himalaya's (`imap | maildir | notmuch`) — `gmail` represents mbx's Gmail HTTP API backend, which himalaya doesn't have. We keep the mechanism, extend the values.

## What we adopt vs. skip vs. extend

| Area | Source shape | Decision |
|---|---|---|
| Per-account block style | `[accounts.<name>]` + dotted keys | **Adopt** |
| Global defaults at top level | `display-name`, `signature*`, `downloads-dir`, ... | **Partial adopt**: `downloads-dir` (closes audit finding #1); skip himalaya's TUI/signature/template knobs entirely. mbx adds `cache-dir`. |
| `default = true` per account | account-level flag | **Skip** — explicitly rejected by CONTEXT.md "no default account". |
| Type discriminator's *position* in the dotted path | `backend.type = ...` (one level down from account) | **Adopt** — moves from current `accounts.<name>.type` up at the account level, down to `backend.type` next to the fields it controls (host, port, encryption, auth). |
| `backend.type` value set | `imap` \| `maildir` \| `notmuch` | **mbx-scoped values**: `imap` \| `gmail`. `gmail` = Gmail HTTP API (not Gmail-over-IMAP); himalaya has no equivalent. Maildir/notmuch out of scope. |
| `backend.encryption.type` | `none` \| `start-tls` \| `tls` | **Adopt**, replaces our flat `backend.tls`. |
| `backend.login` | string at backend level | **Adopt**, replaces our `backend.auth.username` (login is a backend concern, not auth-method-specific). |
| `backend.auth.{type, raw, keyring, cmd, client-*, refresh-token, access-token, auth-url, token-url, method, pkce, scope, scopes, redirect-*}` | full auth block | **Adopt** — already 95% the current mbx shape. |
| `backend.auth.{client-secret,refresh-token,access-token}` as nested secret blocks with their own `.raw/.keyring/.cmd` | tagged-sum-per-secret | **Adopt** — matches ADR-0001 verbatim. |
| `backend.auth.<secret>.write_cmd` | **not in himalaya** | **mbx extension** — ADR-0001's load-bearing addition. Refresh-token still requires it. |
| `backend.extensions.*` | IMAP-only quirks like `id.send_after_auth` | **Adopt opportunistically** — accept under the schema, implement only when a real account needs one. |
| `message.send.backend.*` | full send-backend block (smtp, sendmail) | **Adopt with mbx-scoped values**: `smtp` only. `sendmail` skipped (out of scope). |
| `message.send.backend` for `backend.type = "gmail"` | n/a in himalaya (no gmail backend) | **mbx rule**: forbidden — the Gmail API handles send. Validator rejects. |
| `message.send.save-copy`, `message.send.pre-hook` | message-policy keys | **Schema-accept**, defer implementation to Phase 4 (write path). |
| `message.delete.style = "flag" \| "folder"` | deletion semantics | **Schema-accept**, default `folder`. Phase 4 consumes. |
| `message.read.headers`, `message.read.format` | read-rendering policy | **Skip** — mbx's read output is JSON-shaped, not human-rendered. |
| `message.write.headers` | compose-template policy | **Skip** — mbx has no `$EDITOR`-driven compose (AGENTS.md §11). |
| `template.{new,reply,forward}.*` | compose templates | **Skip** entirely. |
| `pgp.*` | OpenPGP integration | **Skip** for v1 (out of scope). |
| `account.list.table.*`, `*.list.table.*`, `*.list.page-size` | TUI styling and pagination | **Skip** — JSON-only output (ADR-0004); cursor pagination (commands.md). |
| `folder.aliases.{inbox,sent,drafts,trash,*}` | alias map | **Adopt**, validator stores them. Phase 2 read-path uses them where canonical-folder vocab matters. |
| `cache.*` (mbx-only, opt-in derived SQLite) | n/a in himalaya | **Keep** — mbx extension per ADR-0003. Stays under `[accounts.<name>]` namespace. |

## Considered alternatives

- **Keep current section-headers-per-level shape; just rename fields.** Rejected: each account stays as 4–7 separate `[...]` blocks; account-add/remove become file-range edits across blocks; we keep diverging from the reference project we're already lifting from. The decision in this ADR is cheap to make once and expensive to revisit; deferring it past Phase 1 means rewriting handlers we're about to write.
- **Adopt himalaya's shape *verbatim*, including templates / PGP / TUI knobs.** Rejected: those keys would parse but never be consumed; with `DisallowUnknownFields` they'd have to be in the schema to load any himalaya-derived config, which would mislead readers about what mbx implements. We skip them at the schema level and document the absence ("What's deliberately absent" section in docs/config.md).
- **Adopt the shape but keep the type discriminator at the account level (`[accounts.<name>].type = "gmail"|"imap"`) instead of moving it to `backend.type`.** Rejected: the validator's branch logic is keyed by *what kind of backend this is* — required host/port, allowed auth shapes, presence/absence of `message.send.backend.*`. Putting the discriminator one level up from the fields it controls means every validator rule has to reach across levels. Putting it at `backend.type` keeps "everything about how to talk to this server, including what kind of server it is, lives under `backend.*`" — which is what every other dotted key already implies.

## Proposed schema (target shape)

The full TOML, dotted-key form, with all keys mbx will validate. `<>` brackets are placeholders.

### Top-level (global defaults)

```toml
# Optional. Directory mbx writes downloads into when `mbx attachment download
# --output` is not passed. Defaults to the system temp directory.
downloads-dir = "~/Downloads"

# Optional. Root directory for opt-in per-account caches. Defaults to ~/.cache/mbx.
# Per-account [accounts.<name>.cache.path] takes precedence when set.
cache-dir = "~/.cache/mbx"
```

### Per-account block — IMAP example

```toml
[accounts.work]
email = "you@company.com"

# Backend: read side (and, for `backend.type = "gmail"`, send side).
backend.type = "imap"
backend.host = "imap.company.com"
backend.port = 993
backend.encryption.type = "tls"        # "tls" | "start-tls" | "none"
backend.login = "you@company.com"

# Backend auth — password
backend.auth.type = "password"
backend.auth.cmd = "op read op://Dev/mbx-work/password"
# alternative variants:
# backend.auth.raw     = "p@ssw0rd"     # testing only
# backend.auth.keyring = "mbx-work"

# Send backend — SMTP
message.send.backend.type = "smtp"
message.send.backend.host = "smtp.company.com"
message.send.backend.port = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login = "you@company.com"
message.send.backend.auth.type = "password"
message.send.backend.auth.cmd = "op read op://Dev/mbx-work/password"

# Folder aliases — required-default keys: inbox, sent, drafts, trash.
# Extra user aliases are allowed.
folder.aliases.inbox  = "INBOX"
folder.aliases.sent   = "Sent"
folder.aliases.drafts = "Drafts"
folder.aliases.trash  = "Trash"

# Optional cache (mbx extension)
# cache.sync_days = 30
# cache.folders   = ["INBOX", "Sent"]
# cache.path      = "~/.cache/mbx/work.db"   # defaults to <cache-dir>/<name>.db
```

### Per-account block — Gmail example

```toml
[accounts.gmail-personal]
email = "you@gmail.com"

# Backend: Gmail API. Handles both read and send.
backend.type  = "gmail"
backend.login = "you@gmail.com"

# OAuth 2.0
backend.auth.type      = "oauth2"
backend.auth.client-id = "1234.apps.googleusercontent.com"
backend.auth.auth-url  = "https://accounts.google.com/o/oauth2/v2/auth"
backend.auth.token-url = "https://www.googleapis.com/oauth2/v3/token"
backend.auth.method    = "xoauth2"           # "xoauth2" | "oauthbearer"
backend.auth.scopes    = ["https://mail.google.com/"]
# backend.auth.pkce            = true
# backend.auth.redirect-host   = "localhost"
# backend.auth.redirect-port   = 0

# Secret blocks — tagged-sum, plus write_cmd for refresh-token.
backend.auth.client-secret.cmd = "op read op://Dev/mbx-gmail-personal/client-secret"

backend.auth.refresh-token.cmd       = "op read op://Dev/mbx-gmail-personal/refresh-token"
backend.auth.refresh-token.write_cmd = "op item edit mbx-gmail-personal refresh-token[password]=-"

# Optional. Defaults to keyring "<account>-access-token". Recommend keyring
# variant so mbx can write back rotations without a write_cmd.
# backend.auth.access-token.keyring = "mbx-gmail-personal-access-token"

# No `message.send.backend.*` — forbidden for Gmail backends; validator rejects.

folder.aliases.inbox  = "INBOX"
folder.aliases.sent   = "[Gmail]/Sent Mail"
folder.aliases.drafts = "[Gmail]/Drafts"
folder.aliases.trash  = "[Gmail]/Trash"
```

### Validator rules

1. `[accounts.<name>]` requires `email` and `backend.type`.
2. `backend.type = "imap"` requires `backend.host`, `backend.port`, `backend.encryption.type`, `backend.login`, `backend.auth.*`, and `message.send.backend.*` (with the same five fields under it).
3. `backend.type = "gmail"` requires `backend.login`, `backend.auth.*` with `type = "oauth2"`, full OAuth fields including `refresh-token.write_cmd`. **Forbids** `message.send.backend.*`.
4. `backend.auth.type = "password"` requires exactly one of `raw|keyring|cmd` on the same level (not nested), and forbids OAuth fields (`client-id`, `auth-url`, `token-url`, secret blocks).
5. `backend.auth.type = "oauth2"` requires `client-id`, `auth-url`, `token-url`, and a `refresh-token` block with one of `raw | keyring | cmd` set. The `refresh-token.write_cmd` is **not** validated at load time — it is required at `mbx account auth` time per ADR-0001 (`auth.missing_write_cmd`, exit 11). Optional fields on the auth block: `client-secret`, `access-token`, `method`, `pkce`, `scope`/`scopes`, `redirect-*`.
6. `folder.aliases.inbox` is required when `backend.type = "imap"`. The four canonical aliases (`inbox`, `sent`, `drafts`, `trash`) are recommended but only `inbox` is required by the validator. Extra user-defined aliases under `folder.aliases.<name>` are accepted.
7. `cache.*` is optional. If present, `cache.path` defaults to `<cache-dir>/<name>.db`; `cache.sync_days` defaults to `30`; `cache.folders` defaults to `["INBOX"]` (or to the alias resolution of `folder.aliases.inbox`).

## CONTEXT.md vocab deltas

These edits land alongside the code reshape, not in this ADR:

- **Account**: replace "Has exactly one **Backend**; IMAP accounts also have one **Send** block." → "Has exactly one **Backend**. When the backend is IMAP, also has one **Send Backend** (under `message.send.backend`)."
- **Backend**: replace "For Gmail accounts the backend uses the Gmail API and also handles send; for IMAP accounts it speaks IMAP and a separate **Send** block speaks SMTP." → "Distinguished by `backend.type` (`gmail` or `imap`). The `gmail` backend uses the Gmail API for both read and send. The `imap` backend speaks IMAP for read and pairs with a **Send Backend** for write."
- **Send** → rename to **Send Backend**: "The write/transport side of an `imap` **Account**. Lives under `message.send.backend.*`. Omitted (and forbidden) for `gmail` backends. Auth is configured independently — there is no implicit inheritance from `backend.auth`."
- **Provider** entry → remove. It duplicates `backend.type` and isn't load-bearing in commands.
- New entry **Folder Alias**: "User-supplied mapping from a canonical mbx folder role (`inbox`, `sent`, `drafts`, `trash`, or any custom name) to the provider's actual folder name. Configured under `folder.aliases.*`. Used by mbx commands that need to refer to a canonical role (e.g., `message.send.save-copy` writes to the `sent` alias's resolved folder)."
- "Relationships" section:
  - "An **Account** has exactly one **Backend** and zero-or-one **Send** block." → "An **Account** has exactly one **Backend**, and zero-or-one **Send Backend** (required iff `backend.type = "imap"`)."

## Consequences

### Code that breaks (Phase 0 follow-up to land with this ADR's implementation)

- `internal/config/config.go` — `Account`, `Backend`, `Send`, `Auth` structs reshape; new top-level globals; new `Folder` block; validator rules above.
- `internal/account/templates.go` — both templates rewritten.
- `internal/account/configfile.go` — `HasAccount` still works (the `[accounts.<name>]` header line is identical), no change.
- `internal/account/auth/auth.go` — `Config()` signature is unchanged (still takes `*config.Auth`); only the field paths internal to that struct change. PKCE/scope/redirect-* logic is invariant.
- `internal/config/config_test.go` — both fixture configs rewritten in dotted-key form.
- `docs/config.md` — rewritten end-to-end against the new shape.
- `docs/commands.md` — `account list` output shape kept (`{name, type, email, cache}`); `type` now derives from `backend.type` instead of `account.type`. No external contract change.

### Code that does NOT break

- `cmd/mbx/account.go` handlers — read from typed `*config.Config` only; struct reshape is invisible at this layer.
- `internal/account/account.go` — `List`, `Lookup`, `Info` are unaffected; `Info.Type` is sourced from `account.Backend.Type` instead of `account.Type`.
- `internal/secret/` — Secret tagged-sum is identical.
- `internal/mbxid/` — independent of config shape.
- `internal/output/` — independent of config shape.
- ADR-0001 (secrets), ADR-0003 (cache), ADR-0004 (JSON contract) — all reinforced, none invalidated.

### One-way migration

Anyone who has hand-written a `config.toml` against the current mbx shape (which, as of this commit, is just the development author) will need to migrate. The migration is mechanical:

- `[accounts.<name>]` block stays; collapse all `[accounts.<name>.backend.*]` and `[accounts.<name>.send.*]` subheaders into dotted keys under the account header.
- `[accounts.<name>].type` → `backend.type`.
- `backend.tls = "tls"` → `backend.encryption.type = "tls"`.
- `backend.auth.username` → `backend.login`.
- `[accounts.<name>.send]` → `message.send.backend.*` (with `message.send.backend.type = "smtp"`).
- `[accounts.<name>.send.auth]` → `message.send.backend.auth.*` (no inheritance — fill it in).

mbx will emit a structured `config.invalid` with hints when it encounters legacy section headers; we won't ship a migration tool for a single-user codebase pre-1.0.

## Status

Proposed. Pending approval, the implementation lands as one commit reshaping the schema, validator, templates, examples, and tests in lockstep — Phase 1 tasks 1.5–1.7 build against the final shape.
