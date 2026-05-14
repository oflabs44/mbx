# 0002 — Self-describing mbx IDs

Every envelope and thread mbx emits carries a stable, self-describing ID:

- Gmail: `gmail:<account>:<gmail-msg-id>`
- IMAP: `imap:<account>:<folder>:<uidvalidity>:<uid>` where `<folder>` is percent-encoded to escape `:` and other reserved characters

The ID encodes provider, account, and the minimum locator the provider needs. This means single-message commands (`message read`, `envelope flag`, `message move`, ...) do not require `-a`: the account is parsed from the ID. When both an ID and `-a` are passed, mbx validates they agree and errors otherwise.

## Considered alternatives

- **Opaque hash IDs (random per message, stored in cache).** Rejected: requires the cache to be authoritative for ID→provider resolution, contradicting [ADR-0003](./0003-cache-as-derived-state.md). Also makes IDs unstable across machines without cache sync.
- **Provider-native IDs as-is (`18f3c2a...` for Gmail, `1234` for IMAP UID).** Rejected: collide across accounts, lose provider context, force every command to carry `-a` + `--folder` redundantly.
- **Composite without account (`gmail:18f3c2a...`).** Rejected: multi-account fan-out (Q13) needs to know which Gmail account a message came from when results merge.

## Consequences

- `-a` is *required* only on commands that don't take a message ID input: `envelope list`, `envelope search`, `folder list`, `cache sync`, `account auth`. Documented in the README's command-flag table.
- IMAP IDs are sensitive to `UIDVALIDITY`. When a server resets `UIDVALIDITY` (rare but real), all prior mbx IDs for that folder are invalidated. mbx detects this on the next sync and surfaces it via `account doctor`. Skills that hold IDs across long timespans must be tolerant of `provider.id_invalidated` errors.
- IMAP folder names must be percent-encoded inside the ID. mbx does the encoding/decoding; skills treat IDs as opaque.
- Adding a new provider in the future requires picking a new prefix (`proton:`, `exchange-ews:`) and a stable locator scheme.
