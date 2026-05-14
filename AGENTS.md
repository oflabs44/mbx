# Working on mbx

Project-level agent instructions. Read this before writing code, running commands, or proposing design changes. Global rules from `~/AGENTS.md` still apply — this file adds project-specific rules and points to canonical docs.

## 1. Read first

Before any non-trivial change, read in this order:

1. [README.md](./README.md) — overview and doc index.
2. [CONTEXT.md](./CONTEXT.md) — domain language. **Source of truth for terminology.**
3. [docs/commands.md](./docs/commands.md) — command surface contract. **Source of truth for flags and JSON shapes.**
4. [docs/config.md](./docs/config.md) — config file schema. **Source of truth for `~/.config/mbx/config.toml` shape.**
5. [docs/adr/](./docs/adr/) — architectural decisions. Skim titles; read in full any ADR touching the area you're changing.

If a change conflicts with one of those, the doc wins. If you believe the doc is wrong, update it in the same change.

## 2. Stack & tooling

- **Language**: Go 1.26.1.
- **Module**: `github.com/oflabs44/mbx`.
- **Runner**: `make` only. Available targets: `make build | install | run | test | fmt | lint | clean`. Never invoke `go build` / `go test` directly; route through the Makefile so behaviour stays uniform.
- **Format / vet**: `make fmt` (gofmt + `go mod tidy`) and `make lint` (`go vet`) are non-negotiable before reporting a task done.
- **CLI framework**: Cobra (`github.com/spf13/cobra`). See §6 for what does and doesn't belong in a Cobra handler.
- **Config format**: TOML. Parser: decide between `github.com/BurntSushi/toml` and `github.com/pelletier/go-toml/v2` on first add; document the choice in an ADR.
- **Key libraries** (decide and document on first add): `google.golang.org/api/gmail/v1`, `golang.org/x/oauth2`, `github.com/emersion/go-imap` (v2 preferred), `github.com/emersion/go-message`, `github.com/emersion/go-smtp`, SQLite driver TBD.

## 3. Domain language discipline

- Use only terms defined in [CONTEXT.md](./CONTEXT.md). If a needed concept isn't there, add it to CONTEXT.md in the same change rather than coining a synonym in code or docs.
- "mailbox" / "label" / "tag" are not mbx vocabulary in output or commands. Use **Folder**, **Flag**, or **mbx ID** as defined.
- The `envelope` vs `message` distinction is load-bearing: keep cheap and expensive operations on the right noun. If you find yourself fetching a body inside an `envelope` verb, stop and reconsider.

## 4. Architectural invariants

These are non-negotiable without a new ADR superseding the existing one:

- **Secrets**: never compile in a specific provider SDK. Always go through the `raw | keyring | cmd` (read) + `write_cmd` (rotated persistence) shape. ([ADR-0001](./docs/adr/0001-secrets-resolution-model.md))
- **mbx IDs**: self-describing, stable, percent-encoded for IMAP folders. Single-message commands derive the account from the ID. ([ADR-0002](./docs/adr/0002-self-describing-message-ids.md))
- **Cache**: derived state. Never authoritative. Live verbs never read from cache. Write-through is best-effort and never blocks command exit. ([ADR-0003](./docs/adr/0003-cache-as-derived-state.md))
- **JSON contract**: every command emits `{ "v": 1, ... }` on stdout (success) or stderr (error) by default. No TTY-detection. Error `code` field is stable; `message` is not. ([ADR-0004](./docs/adr/0004-json-output-contract.md))
- **No daemon, no IDLE, no editor integration.** mbx is one-shot per invocation. Anything that needs a long-running process belongs in an external tool (cron, systemd-timer, etc.).

## 5. Go conventions

### Proverbs that apply here

- **"Don't communicate by sharing memory; share memory by communicating."** The only meaningful concurrency in mbx is per-account fan-out (`-a a,b,c`). Use goroutines + channels for that. Reach for `sync.Mutex` only when channels are demonstrably wrong (rare in this codebase).
- **"The bigger the interface, the weaker the abstraction."** Don't define a giant `Provider` interface that every backend must fully implement. Prefer many small, capability-scoped interfaces (`EnvelopeLister`, `MessageReader`, `Sender`, `ThreadSearcher`) defined next to the consumer. See §6.
- **"Make the zero value useful."** Where possible, structs should be usable without explicit initialization. Avoid required-field constructors when a zero value can mean "default."
- **"A little copying is better than a little dependency."** Before pulling a library for ~50 lines of logic, write the 50 lines. The threading algorithm port from himalaya is an example: don't pull `gonum/graph`; write the adjacency list.
- **"Clear is better than clever."** No reflection unless unavoidable. No interface-soup. No `any` where a concrete type works.
- **"Errors are values."** Handle them where they happen; don't reflexively bubble `if err != nil { return err }` without context.

### Errors

- Wrap with `%w` when bubbling: `fmt.Errorf("fetching envelope %s: %w", id, err)`. Reserve `%v` / `%s` for log messages.
- Define sentinel errors as package-level vars (`var ErrAccountNotFound = errors.New("...")`) only when callers need to branch on them via `errors.Is`. Otherwise, plain wrapped errors are enough.
- The user-facing JSON `error.code` is **not** the Go error value — it's an output concern. Map Go errors to codes in the output layer, not throughout the codebase. Internal code shouldn't know about `auth.refresh_failed` as a string.
- `panic` is reserved for genuinely unrecoverable conditions (corrupt internal invariants). Never `panic` on user input or network failure.

### Context

- Every operation that does I/O takes `ctx context.Context` as its first parameter.
- Never store `context.Context` in a struct. Pass it explicitly.
- The top-level Cobra handler creates the root context with signal handling (`signal.NotifyContext`) and a deadline if `--timeout` is set. All downstream calls receive it.

### Packages

- Package names: short, lowercase, singular, no underscores. `package envelope`, not `package envelopes` or `package envelope_lib`.
- Constructor naming: `New<Type>` returns `*Type` or `Type`. Drop the `New` prefix only when the type name already implies construction (`bytes.Buffer{}` is fine, no `NewBuffer` needed).
- Avoid `init()` functions. They run before `main`, hide control flow, and complicate testing.
- Avoid global mutable state. Pass dependencies explicitly.

### Testing

- Table-driven tests are idiomatic in Go. Default to them unless a single-case test reads dramatically clearer.
- Test files live next to the code they test (`envelope_test.go` beside `envelope.go`). External test packages (`package foo_test`) only when you want to enforce API boundaries.
- Avoid mocking the world. Prefer narrow interfaces (per §6) and fake implementations over `testify/mock`.

## 6. Project layout: thin handlers, deep core

### Directory structure

```
cmd/mbx/                # Cobra entrypoint + handlers (thin)
internal/
  account/              # account config + lifecycle (auth, doctor)
  envelope/             # envelope domain: list, search, thread, flag
  message/              # message domain: read, send, reply, forward, move, copy, delete
  folder/               # folder domain
  attachment/           # attachment domain
  cache/                # SQLite cache: schema, sync, queries
  config/               # TOML loading, validation
  secret/               # raw | keyring | cmd | write_cmd resolution
  provider/             # provider implementations (gmail, imap) — NOT a kitchen-sink interface
    gmail/
    imap/
  mbxid/                # mbx ID parsing, encoding, validation
  output/               # JSON envelope writer, error → code mapping, table renderer
```

### Thin handlers rule

A Cobra handler does only this:

1. Parse flags into a typed request struct.
2. Resolve config + account.
3. Call **one** domain function in `internal/<domain>/`.
4. Hand the result to the output writer.
5. Return. The handler itself contains no domain logic, no provider calls, no parsing of mail content, no SQL.

If you find yourself writing more than ~30 lines in a handler, move the logic into the corresponding `internal/<domain>/` package.

### Interfaces live with the consumer

Go convention, and load-bearing here. **Don't define a kitchen-sink `Provider` interface in `internal/provider/`** that every backend must implement in full. That's the Java / interface-up-front trap.

Instead:

- In `internal/envelope/list.go`, define the narrow interface that `List` needs:
  ```go
  type Lister interface {
      ListEnvelopes(ctx context.Context, q ListQuery) ([]Envelope, string, error)
  }
  ```
- The `gmail` and `imap` packages in `internal/provider/` implement methods. The interface they satisfy is named *and defined* by the consumer, not by them.
- A backend that doesn't implement a capability (e.g. IMAP without `THREAD`) simply doesn't satisfy `ThreadSearcher`. The domain code branches on `if t, ok := backend.(envelope.ThreadSearcher); ok { ... } else { fallback }`.

This keeps the provider packages free of speculation about future operations and lets the domain layer evolve its needs without touching every backend.

### Where the line is

- **In `internal/<domain>/`**: pure logic — parsing, normalization, query building, result merging, threading algorithm, MIME extraction. No flags, no Cobra, no `os.Exit`, no `os.Stdout`.
- **In `internal/provider/<name>/`**: protocol-specific I/O — Gmail API calls, IMAP commands, SMTP send. Translates between provider-native types and the domain layer's normalized types. No flags, no Cobra.
- **In `cmd/mbx/`**: flag parsing, dispatch, output formatting. Knows about Cobra and `os.Stdout`/`os.Stderr`. Knows nothing about IMAP UIDs or Gmail thread ids.

## 7. Project code style

(Project-specific overrides on top of standard `gofmt` style.)

- No emojis anywhere — code, comments, commits, docs, error messages.
- Default to no comments. Only write a comment when the **why** is non-obvious (a workaround, a subtle invariant, a hidden constraint). Never narrate the **what**.
- Provider-specific concepts that need to leak into output go under a namespaced subobject (`gmail.*`, `imap.*`) in JSON — not at the top level. The top level is the normalized domain shape.
- Error messages on stderr go through `internal/output`'s structured error writer; don't `fmt.Fprintln(os.Stderr, ...)` directly outside that package.

## 8. Post-task workflow

**After any code change, before reporting the task done**, run in this order:

1. `make fmt && make lint && make test` — must all pass.
2. Invoke the `pr-review-toolkit:code-simplifier` agent on the diff (or on the changed files when not yet committed). It will look for unnecessary abstractions, duplicated logic, and dead code.
3. Invoke the other **pr-review-toolkit** agents on the diff. Always run these two:
   - `pr-review-toolkit:code-reviewer` — checks adherence to this file, CONTEXT.md, and the ADRs.
   - `pr-review-toolkit:silent-failure-hunter` — mbx does heavy error-path work (auth, network, parse, MIME); silent failures are a real risk.

   Run these conditionally when relevant to the change:
   - `pr-review-toolkit:type-design-analyzer` — when adding or modifying Go interfaces or non-trivial types.
   - `pr-review-toolkit:comment-analyzer` — when adding or modifying multi-line comments or doc comments.
   - `pr-review-toolkit:pr-test-analyzer` — when adding new functionality with test coverage implications.

4. **Filter findings through the over-engineering lens before acting on them.** Reviewers — especially `silent-failure-hunter` and `code-reviewer` — bias toward defensive additions: extra validation, fallback paths, speculative error handling. For each finding, ask: does the failure mode this fix prevents actually exist in practice given our current architecture, or is it a hypothetical class of bugs we've explicitly chosen not to defend against? Apply real fixes; skip the ones that exist only because a reviewer thought of them.
5. Address the surviving findings in the same change. If a finding is intentional or knowingly skipped, document why (ADR-worthy → write one; otherwise a tight code comment).

Skip steps 2–3 only for trivial changes (typo fixes in docs, README link tweaks). A change that touches `.go` files always runs them.

## 9. Git identity

This repo lives under `~/code/oflabs44/` and is a **personal** project. The user's git `includeIf` routes commits to `femi.dayo@pm.me`. The remote uses the `github-personal` SSH alias and authenticates as the `oflabs44` GitHub account. Don't push, switch identities, or run `gh pr create` without explicit user instruction — see global `~/AGENTS.md` §7 for the protocol.

## 10. When to write an ADR

Follow the criteria in [docs/adr/](./docs/adr/) (and the global plugin format). All three must be true: hard to reverse, surprising without context, real trade-off. Most changes don't need one. When in doubt, don't write one — write code and update CONTEXT.md or docs/commands.md instead.

## 11. Reference projects

Two prior-art codebases informed mbx's design. Grep them when implementing a feature that has a counterpart there.

### imbox — `~/code/personal/imbox`

TypeScript / Bun email CLI by the same author. Similar problem space, different runtime. **Lift**: live-first architecture, command surface intuitions, MIME parsing patterns via `postal-mime`, provider-abstraction shape. **Don't lift**: 1Password SDK coupling (we generalized to `cmd` / `write_cmd`), JSON config format (we use TOML), TTY-detect output (we always emit JSON), `--body` flag as the only send-input mechanism (we layer `--body` / `--body-file` / `--body-stdin`).

### pimalaya/himalaya — Rust

Reference CLI: <https://github.com/pimalaya/himalaya>. **Lift**: TOML config shape (`backend.auth.*` and per-secret `raw | keyring | cmd` variants), command structure (envelope-vs-message split, `account doctor`, `expunge` vs `purge`), threading algorithm (port from `pimalaya/core/email/src/email/envelope/thread/`). **Don't lift**: editor-driven send flow (`$EDITOR` doesn't fit agent use), keyring-as-only-OAuth-persistence (we close this gap with `write_cmd`).

### pimalaya/core — Rust

The email library under himalaya: <https://github.com/pimalaya/core>. When implementing threading, MIME normalization, or IMAP capability handling, read the corresponding subdirectory of `email/src/email/` first. The algorithms are sound; the Rust→Go port is usually mechanical.
