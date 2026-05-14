# 0001 — Secrets resolution model

mbx adopts himalaya's tagged-sum-per-secret model (`raw` | `keyring` | `cmd`) verbatim, and extends it with a symmetric `write_cmd` for OAuth refresh tokens. Every account's password, OAuth client secret, access token, and refresh token is supplied via one of those variants; for OAuth, mbx invokes `write_cmd` on rotation to persist the new token to whatever store the user prefers (1Password, `pass`, Bitwarden, keychain, etc.).

This is the load-bearing decision that lets mbx be both *unopinionated about secrets providers* and *never write secrets to disk*. `cmd` covers any external resolver. `write_cmd` closes himalaya's known gap (issue [pimalaya/himalaya#582](https://github.com/pimalaya/himalaya/issues/582)) where rotated OAuth tokens can only be persisted to the OS keyring.

## Considered alternatives

- **Hard-code a single secrets SDK (imbox's choice with 1Password).** Rejected: every user must adopt the same tool. mbx is single-user but the user already moves between secret stores by context.
- **`raw` and `keyring` only (himalaya's effective baseline).** Rejected: requires every user to use the OS keychain even if they have all other secrets in 1Password. The `cmd` variant in himalaya makes the read path work but the rotated-token write path remains keychain-only.
- **URI-scheme single field (`password = "op://..."`).** Rejected: same shape as the tagged-sum but loses the ability to declare `keyring` and `raw` without inventing more schemes; less idiomatic in TOML.

## Consequences

- `account auth` for any OAuth account refuses to run if `backend.auth.refresh-token.write_cmd` is unset. Friendly error message lists copy-pasteable recipes for common stores.
- The OS keyring is *not* a built-in fallback. It's just another valid `write_cmd` target (`security add-generic-password -a $USER -s mbx-<acc> -w` on macOS). mbx has no compiled-in keyring backend in v1.
- The "no secrets on file" promise is honored only if the user doesn't choose `raw`. mbx supports `raw` for testing but never recommends it in docs.
