# mbx Context

mbx is a CLI tool for AI-driven email work. It is invoked per-command from skills (Claude Code and similar). This document defines the domain language used throughout the codebase, docs, and skill prompts.

## Language

### Account & transport

**Account**:
A named entry in the user's config representing a single mailbox. Has exactly one **Backend**; IMAP accounts also have one **Send** block. Identified by a user-chosen short name (`work`, `gmail-personal`).
_Avoid_: profile, mailbox, identity.

**Backend**:
The read/fetch side of an **Account**. For Gmail accounts the backend uses the Gmail API and also handles send; for IMAP accounts it speaks IMAP and a separate **Send** block speaks SMTP.
_Avoid_: provider config, imap config, source.

**Send**:
The write/transport side of an IMAP **Account**. Omitted for Gmail accounts (the Gmail API handles send). Auth defaults to inheriting from `backend.auth` unless overridden.
_Avoid_: smtp config, outgoing.

**Provider**:
Implementation type of a **Backend**: `gmail` (Gmail API + XOAUTH2) or `imap` (IMAP+SMTP, including Proton via the local bridge). Surface as the `provider` field in JSON output.
_Avoid_: backend type, transport.

### Messages, envelopes, folders

**Envelope**:
A lightweight representation of a message: id, flags, a handful of headers (From, To, Subject, Date), snippet. Cheap to fetch in bulk. Returned by `mbx envelope *` commands.
_Avoid_: header, summary, listing.

**Message**:
The full content of a piece of mail: envelope fields plus body and attachment metadata. Expensive to fetch. Returned by `mbx message read`. Attachments are referenced by id; their content is fetched via `mbx attachment download`.
_Avoid_: email, mail.

**Folder**:
A container that an **Envelope** belongs to. An Envelope can belong to one or more Folders. For Gmail, "folder" = any user/system label that is not a flag-mapped system label (`UNREAD`, `STARRED`, etc.). For IMAP, "folder" = mailbox. The `folders` field in JSON output is always an array.
_Avoid_: mailbox, label, directory.

**Label** (Gmail-only, not in mbx normalized output):
Gmail's native concept. Some labels become **Folders** in mbx output (e.g. `INBOX`, `Sent`, user labels), some become **Flags** (`UNREAD` → `unread`). Surfaced under the `gmail.labels` provider-extras namespace for callers that need the raw list.

**Flag**:
A boolean status attached to an **Envelope**: `seen`, `flagged`, `answered`, `draft`, `deleted`. Cross-provider vocabulary. mbx normalizes from Gmail system labels and IMAP `\\*` flags into this vocabulary.
_Avoid_: tag, status.

**Thread**:
A graph of related **Envelopes** linked by `In-Reply-To` / `References` headers (IMAP) or Gmail's native `threadId`. Returned by `mbx envelope thread`.
_Avoid_: conversation.

### Identity

**mbx ID**:
A stable, self-describing identifier for an **Envelope** or **Thread**. Format:
- Gmail envelope: `gmail:<account>:<gmail-msg-id>`
- IMAP envelope: `imap:<account>:<folder>:<uidvalidity>:<uid>` (folder name percent-encoded)
- Thread: same shape, with the thread anchor's id

Self-describing so single-message commands (`message read`, `envelope flag`, ...) don't need a separate `-a` flag. mbx parses; skills treat as opaque.
_Avoid_: message id, uid, gmail id.

### Storage

**Cache**:
An optional per-account SQLite store that mirrors a subset of an **Account**'s envelopes and message bodies. Derived state — never authoritative; can be deleted and rebuilt with `mbx cache sync`. Live API calls remain the default; the cache is accessed only via `mbx cache *` commands.
_Avoid_: store, database, local copy.

**Write-through**:
After a mutating remote write (`envelope flag`, `message move`, `message delete`) succeeds, mbx best-effort updates the corresponding cache row. Failure to update the cache never blocks command exit. `send`, `reply`, and `forward` do not write-through.

### Secrets

**Secret**:
A piece of confidential material (password, OAuth client secret, access token, refresh token) referenced from config. Each Secret is supplied via one of three variants (himalaya-style tagged sum): `raw` (inline, testing-only), `keyring` (OS keychain), or `cmd` (any external resolver via a shell command — stdout is the secret value).
_Avoid_: credential, key.

**write_cmd**:
A shell command mbx invokes to *persist* a rotated secret back to the user's chosen store. mbx pipes the new value to the command's stdin. Mandatory for OAuth `refresh-token` on any account; mbx refuses `account auth` for accounts that lack it. Pairs with `cmd` (read) on OAuth refresh-token blocks.
_Avoid_: write hook, persist callback.

## Relationships

- An **Account** has exactly one **Backend** and zero-or-one **Send** block.
- A **Backend** has exactly one auth section; for OAuth, every rotated **Secret** has a paired **write_cmd**.
- An **Envelope** belongs to exactly one **Account** and one-or-more **Folders**.
- A **Message** is the full-content view of exactly one **Envelope**; the two share an **mbx ID**.
- A **Thread** groups one-or-more **Envelopes** from the same **Account**.
- A **Cache** mirrors zero-or-more **Envelopes** of exactly one **Account**.
- A **Folder** belongs to exactly one **Account**.
- A **Flag** applies to exactly one **Envelope**.

## Example dialogue

> **Skill author:** "After `mbx envelope list -a work,personal --unread`, how do I mark one read?"
>
> **mbx expert:** "Pick its `id` from the response and pass it to `mbx envelope flag <id> --add seen`. The id is an **mbx ID** so it already encodes which account; you don't repeat `-a`."
>
> **Skill author:** "And the cache?"
>
> **mbx expert:** "The remote write happens immediately. mbx then **write-throughs** to the **Cache** if one is configured for that account. If the cache update fails, the command still exits 0 — the **Cache** is derived, not authoritative."
>
> **Skill author:** "What about threading on the work account? It's a corporate IMAP."
>
> **mbx expert:** "`mbx envelope thread <id>` probes the server's IMAP CAPABILITY. If THREAD is advertised, the server does the work. Otherwise mbx falls back to its ported algorithm over a bounded window (default 1000 most-recent envelopes in the folder)."

## Flagged ambiguities

- "label" was being used for both Gmail's labels and mbx's normalized concept. Resolved: mbx's normalized concept is **Folder** (multi-valued array); Gmail's raw label list surfaces only under `gmail.labels`.
- "mailbox" is IMAP terminology for what mbx calls **Folder**. Don't surface "mailbox" in JSON output or commands.
- "default account" was considered and explicitly rejected: `-a` is required on commands that don't take an **mbx ID**; no implicit default, no env fallback.
