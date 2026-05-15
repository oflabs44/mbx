# mbx

Agent-first email CLI. Written in Go.

mbx is built to be driven from a script or an LLM skill, not from a
terminal. Every command takes typed flags, emits one JSON object on
stdout (or stderr on failure), and returns a stable exit code from a
small published taxonomy. There is no interactive prompt, no editor
integration, no daemon.

## Install

```bash
git clone https://github.com/oflabs44/mbx ~/code/mbx
cd ~/code/mbx
make install
```

`make install` runs `go install ./cmd/mbx` and reports the resolved
binary path (usually `$(go env GOPATH)/bin/mbx`). Add that directory to
`$PATH` if it isn't already.

Requires Go 1.26+. No cgo, no external SQLite library — the build is
hermetic.

Optional, after install:

```bash
mbx completion zsh > "${fpath[1]}/_mbx"   # or bash / fish / powershell
```

## Quickstart — first Gmail account

1. **Scaffold the config block.** The file lives at
   `~/.config/mbx/config.toml` (override with `-c` or `$MBX_CONFIG_DIR`).

   ```bash
   mbx account add gmail-personal --type gmail
   ```

   Open the file, fill in `email`, `backend.login`, and the OAuth
   credentials. The secret blocks support inline `raw` (testing only),
   `keyring`, or `cmd` (shell command whose stdout is the secret). For
   the rotated `refresh-token`, set `write_cmd` too — mbx pipes the
   refreshed token into its stdin. Recipes for 1Password, `pass`, and
   the macOS keychain live in [`docs/config.md`](./docs/config.md).

2. **Run the OAuth flow once.**

   ```bash
   mbx account auth gmail-personal
   ```

   Browser opens, you grant consent, mbx persists the refresh token
   through `write_cmd` and exits 0. From here on, every command reuses
   the rotated token transparently.

3. **First triage loop.**

   ```bash
   mbx envelope list -a gmail-personal --unread --limit 10
   ```

   Pick an `id` from the response and mark it read:

   ```bash
   mbx envelope flag <id> --add seen
   ```

   Read its body:

   ```bash
   mbx message read <id>
   ```

   Archive it (Gmail = remove INBOX label):

   ```bash
   mbx message move <id> Archive
   ```

4. **Verify the account end-to-end.**

   ```bash
   mbx account doctor gmail-personal
   ```

   Reports config validity, secret resolution, OAuth refresh,
   connectivity, and server capabilities. Run after any config change
   that touches auth.

## Optional: enable the cache

The cache is opt-in derived state — live verbs never read it; you query
it through the parallel `mbx cache *` surface. Useful for fast local
search and offline triage of previously-pulled envelopes.

```toml
# Per-account, in ~/.config/mbx/config.toml
[accounts.gmail-personal]
# ...
cache.sync_days = 30
cache.folders   = ["INBOX", "Sent"]
```

```bash
mbx cache sync   -a gmail-personal
mbx cache list   -a gmail-personal --unread --limit 50
mbx cache status -a gmail-personal
```

Mutating live verbs (`envelope flag`, `message move`, `message delete`)
write through best-effort; failures never block command exit
([ADR-0003](./docs/adr/0003-cache-as-derived-state.md)).

## Docs

- [Command reference](./docs/commands.md) — full surface, flags, JSON shapes, exit codes
- [Config reference](./docs/config.md) — schema for `~/.config/mbx/config.toml`, secret block recipes
- [Domain language](./CONTEXT.md) — terms used throughout the codebase
- [Architecture decisions](./docs/adr/) — load-bearing design records
- [Changelog](./CHANGELOG.md) — release history
- [Implementation plan](./docs/plan.html) — phased task tracker (open in browser)

## Status

v0.1.0 ships read + write + cache + multi-account fanout for Gmail and
IMAP (Proton via bridge included). The `mbx-assistant` Claude Code
skill that drives mbx from an agent lives outside this repo. There is
no roadmap beyond v0.1 yet; feature requests want an ADR before code.
