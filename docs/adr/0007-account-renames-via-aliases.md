# 0007 — Account renames via alias lists

mbx IDs embed the account name (`gmail:<account>:<id>`, `imap:<account>:<folder>:<uidv>:<uid>` — [ADR-0002](./0002-self-describing-message-ids.md)). That means a rename — renaming `personal` to `personal-gmail` in the config — invalidates every previously-emitted ID by default: agent memory, skill caches, conversation history, log scrapes.

To make rename safe without changing the ID format, each account gains an optional `aliases` list. The alias acts as a secondary address for lookup; new IDs are stamped with the canonical name.

```toml
[accounts.personal-gmail]
aliases = ["personal"]
backend.type = "gmail"
# ...
```

`mbx account rename <old> <new>` is the verb that performs the rewrite: rename the TOML section, append `<old>` to `aliases`, write atomically.

## Lookup semantics

`account.Lookup(cfg, name)` returns the **canonical name** plus the account. Two map reads, both O(1):

1. Direct hit on `cfg.Accounts[name]` → return `(name, acct)`.
2. Otherwise: look up `name` in the precomputed `aliasToCanon` map → return `(canonical, cfg.Accounts[canonical])`.
3. Otherwise: `ErrAccountNotFound`.

The `aliasToCanon` map is built once at config load. Collisions (one alias claimed by two accounts; an alias colliding with a canonical name) surface at that point as `config.invalid` (exit 40), not at the first command that uses them.

## Canonical-on-emit

When an old ID is passed to a verb, the backend resolves through the alias and then **re-stamps emitted IDs with the canonical name**. Calling `mbx envelope thread gmail:personal:19e2...` on a renamed account returns envelopes carrying `gmail:personal-gmail:19e2...`. Over time, every stored reference migrates to the canonical name through normal use; the alias is rear-view-mirror compatibility.

## Considered alternatives

- **Stable opaque account IDs (UUID or content-hash) embedded in mbx IDs instead of the name.** Rejected: `gmail:a7f3:18f...` loses the human-readable account context skills and `git grep` rely on; requires a one-way ID-format migration of every prior ID; doesn't compose with [ADR-0002](./0002-self-describing-message-ids.md)'s self-describing intent.
- **No rename — document it as "remove + re-add, accept the data loss."** Rejected: low cost today, high cost the moment you hold a thread ID in a long-running skill. Aliases are ~50 LOC and one verb; the data-loss alternative is permanent.
- **Aliases without `mbx account rename`** — make users hand-edit TOML. Rejected: rename has a load-bearing invariant (alias must not collide with an existing canonical name) that a verb can validate atomically. Hand-edits silently produce a `config.invalid` only at the next command.

## Consequences

- `config.Account` gains `Aliases []string` (TOML key `aliases`). Optional; default empty.
- `config.Config` gains an unexported `aliasToCanon map[string]string`, populated at load.
- New error class — `config.invalid` is reused for alias collisions; the message names both conflicting accounts.
- `account.Lookup` signature changes from `(*Account, error)` to `(canonicalName string, acct *Account, err error)`. Callers that re-stamp IDs (gmail/imap backends) use the canonical name; callers that just want the account ignore it. Single in-tree call site change.
- `mbx account rename` — new verb under `[mbx account]`. Validates the new name is free and isn't an alias of anything else, rewrites the file atomically (same TOML-edit pattern `account add`/`account remove` use).
- Cache rows under the old name continue to work because `mbx cache *` verbs go through `Lookup` too, but new write-throughs land under the canonical name; running `mbx cache clear -a <new>` + `mbx cache sync -a <new>` is the recommended migration.
- IMAP folder renames are a structurally identical problem (the folder segment is in the ID). They are **not** in scope for this ADR; `folder.aliases.*` already exists for canonical-role mapping, and extending it to support old-folder-name → new-folder-name aliases is a follow-up if and when needed.

## Status

Proposed. Implementation lands as one commit covering `internal/config`, `internal/account`, `cmd/mbx/account.go`, docs (`commands.md`, `config.md`, `CONTEXT.md`), and tests.
