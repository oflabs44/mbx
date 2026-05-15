# mbx

Agent-first email CLI. Written in Go.

mbx is built to be driven from a script or an LLM skill, not from a
terminal. Every command takes typed flags, emits one JSON object on
stdout (or stderr on failure), and returns a stable exit code from a
small published taxonomy. There is no interactive prompt, no editor
integration, no daemon.

## Install

```bash
git clone https://github.com/oflabs44/mbx
cd mbx
make install
```

Requires Go 1.26+. The build is pure-Go (no cgo, no external SQLite).
`make install` runs `go install ./cmd/mbx` and prints the resolved
binary path.

## Quickstart

mbx reads everything it needs from a single TOML config file. By
default mbx looks at `$XDG_CONFIG_HOME/mbx/config.toml`, falling back
to `~/.config/mbx/config.toml`. Override with `-c <path>` or
`$MBX_CONFIG_DIR`.

A minimal Gmail-only config:

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

backend.auth.client-secret.cmd       = "op read op://Dev/mbx-gmail/client-secret"
backend.auth.refresh-token.cmd       = "op read op://Dev/mbx-gmail/refresh-token"
backend.auth.refresh-token.write_cmd = 'op item edit mbx-gmail "refresh-token=$(cat)" </dev/null'
```

Two things to understand:

- **Secrets are resolved on demand.** Every confidential value
  (`client-secret`, `refresh-token`, IMAP password) sets exactly one of
  `raw` (testing), `keyring`, or `cmd` (any shell command — stdout is
  the secret). Nothing lands on disk through mbx itself.
- **OAuth refresh tokens rotate.** Any block carrying a rotated secret
  must also set `write_cmd`. mbx pipes the new value into its stdin
  after a refresh. Recipes for 1Password, `pass`, and the macOS
  keychain live in [`docs/config.md`](./docs/config.md).

`mbx account add <name> --type gmail|imap` will scaffold a commented
template if you'd rather edit one in place. After editing:

```bash
mbx account auth gmail-personal     # one-time OAuth consent
mbx account doctor gmail-personal   # verifies config + auth + connectivity
mbx envelope list -a gmail-personal --unread --limit 10
```

IMAP accounts (including Proton via the local bridge) are configured
the same way with `backend.type = "imap"` and a parallel
`message.send.backend` block for SMTP — see
[`docs/config.md`](./docs/config.md) for the full shape.

## Docs

- [Command reference](./docs/commands.md) — full surface, flags, JSON shapes, exit codes
- [Config reference](./docs/config.md) — schema, secret block recipes
- [Changelog](./CHANGELOG.md) — release history
