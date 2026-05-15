# 0009 — `message archive` verb and `archive` as a canonical folder role

mbx surfaces folder mutations via `message move <id>... <folder>` ([commands.md](../commands.md#mbx-message-move-id-folder)). On Gmail, "archive" in the UI means *remove the `INBOX` label and add nothing else* — there is no `Archive` folder to move to. On IMAP, "archive" means *MOVE to whatever the user calls their archive folder* (`Archive`, `Archives/2026`, `[Gmail]/All Mail` for IMAP-Gmail, …).

`message move` cannot express the Gmail half: it always adds a destination label. Earlier docs (pre-issue #1) advertised `mbx message move <id> "Archive"` as if Gmail had an `Archive` label; it doesn't, and the call always 400'd. Issue #1 fixed the *name → id* resolution bug; this ADR fixes the *no-destination* gap.

mbx adds a new verb, `mbx message archive <id>...`, and promotes `archive` to a canonical folder role alongside `inbox`, `sent`, `drafts`, `trash`.

```bash
mbx message archive gmail:work:18f3...
mbx message archive imap:work:INBOX:1:42 imap:work:INBOX:1:43
```

## Provider semantics

- **Gmail**: `users.messages.modify` with `removeLabelIds=["INBOX"]`, no `addLabelIds`. The mbx ID is unchanged. If the message isn't in INBOX, the operation is a server-side no-op. No `folder.aliases.archive` lookup — Gmail's archive is purely INBOX-removal.
- **IMAP**: MOVE to `folder.aliases.archive`. Resolution mirrors the existing `trash` rule: if `folder.aliases.archive` is unset for the account, return `config.invalid` (exit 40) with a message naming the missing key. Mbx never silently picks an archive folder. New IDs are emitted from `COPYUID` where available (UIDPLUS / IMAP4rev2).

This parallels the existing `message delete` (no `--permanent`) shape:

| Verb | Gmail | IMAP |
|---|---|---|
| `message delete` | Trash label (`users.messages.trash`) | MOVE to `folder.aliases.trash`; `config.invalid` if unset |
| `message archive` | Remove `INBOX` label | MOVE to `folder.aliases.archive`; `config.invalid` if unset |

## JSON shape

`message archive` reuses the `mutateResult` shape used by `move`/`copy`/`delete`:

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

On Gmail, `dest` is omitted (the ID is stable and there is no destination folder):

```json
{
  "v": 1,
  "data": {
    "ids": ["gmail:work:18f3..."],
    "new_ids": ["gmail:work:18f3..."]
  },
  "meta": { "accounts_queried": ["work"] }
}
```

Multi-ID input is **fail-fast**, matching `move`. The Gmail diff is idempotent; the IMAP MOVE is not (re-running after partial failure 404s the moved ids — re-list to recover).

## Config schema

`folder.aliases.archive` joins `inbox`, `sent`, `drafts`, `trash` as a recognized canonical role. Like `trash`, it is:

- Optional at config-load time.
- Required at command time for `imap` accounts that invoke `message archive` (validator does not pre-flag absence — only the verb does).
- Ignored by `gmail` accounts.

The JSON Schema for `config.toml` ([config.md](../config.md)) gets `archive` added to the alias key examples; no breaking change to existing configs.

## Considered alternatives

- **`mbx message move <id> --archive` flag.** Rejected: `move` requires a positional `<folder>`; adding a flag that suppresses the positional makes the surface ambiguous (`move <id> --archive` vs `move <id> Archive --archive`). Surface friction outweighs the one-verb saving.
- **Sentinel destination: `mbx message move <id> @archive` (or `:archive:`).** Rejected: overloads the `<folder>` positional, collides with any folder name starting with the sentinel character, and forces every reader of `commands.md` to learn a sentinel grammar. A new verb is cheaper to document and discover.
- **`mbx envelope flag --remove INBOX <id>...`.** Rejected on two counts. First, `INBOX` is a Folder in mbx vocabulary, not a Flag (the Flag vocabulary is `seen, flagged, answered, draft, deleted` — [commands.md §envelope flag](../commands.md#mbx-envelope-flag-id)). Second, it leaks Gmail's internal label model into user-facing commands; the whole point of `message archive` is to provide a normalized verb that hides the Gmail-vs-IMAP split.
- **Gmail-only verb, IMAP users keep using `move`.** Rejected: the user-facing benefit is *one verb, both providers*. A Gmail-only verb still leaves IMAP users typing the literal archive folder name and tying their tooling to that string instead of the role.
- **Add `archive` as a Flag instead of a Folder role.** Rejected: archiving isn't a flag transition on either provider — it's a folder/label operation. Coercing it into the Flag vocabulary inverts CONTEXT.md's noun split.

## Consequences

- New verb `mbx message archive <id>...` with `mutateResult` JSON shape.
- New narrow consumer interface `message.Archiver` in `internal/message/mutate.go`, satisfied by both `internal/provider/gmail/` and `internal/provider/imap/`. Following CLAUDE.md §6, the interface is defined where it is consumed; the gmail and imap packages don't import it.
- `folder.aliases.archive` recognized by the validator; documented in `config.md`. No required-default rule.
- `docs/commands.md` gets a `mbx message archive <id>...` section between `delete` and the section that follows, plus a callout in `move`'s Gmail-provider note pointing at it.
- The "Gmail has no Archive label" note added to `move` in Phase 1 is updated to point at the new verb.

## Implementation notes (for the reviewer)

- The Gmail implementation is `c.modifyAll(ctx, ids, &gmailv1.ModifyMessageRequest{RemoveLabelIds: []string{"INBOX"}})` — no `labelIDByName` round-trip needed (INBOX is a system label).
- The IMAP implementation reuses the existing `MoveMessages` path; only the destination resolution differs. Factoring a tiny `resolveArchiveFolder` mirrors `resolveTrashFolder`.
- No cache changes: the existing write-through path for `move`/`delete` covers `archive` once the cmd handler calls `cacheInvalidateAfterMutation`.
