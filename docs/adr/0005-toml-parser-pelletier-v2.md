# 0005 — TOML parser: pelletier/go-toml/v2

mbx uses `github.com/pelletier/go-toml/v2` for parsing the config file. Chosen over `github.com/BurntSushi/toml` for line/column-aware error messages on decode failure and the `Decoder.DisallowUnknownFields` option for catching typos in user configs.

## Considered alternatives

- **`github.com/BurntSushi/toml`** — the older, more popular Go TOML library. Equally mature and stable. Rejected because its decode errors are less actionable: `account doctor` and the config-load path are user-facing surfaces where "line 14 column 3: expected `=` after key" beats "toml: cannot parse" by a wide margin.
- **`encoding/toml` (stdlib)** — does not exist yet. The TOML proposal for the standard library has been on the table for years and has not landed.

## Consequences

- Config decode errors surface line+column via `pelletier/go-toml/v2`'s `DecodeError` type. The config-load path in `internal/config/` wraps this into a `config.invalid` failure with the position carried in `error.details`.
- Strict mode (`Decoder.DisallowUnknownFields(true)`) catches typos like `backend.auth.passwd_cmd` (instead of `password_cmd`). mbx enables it for the config file.
- If pelletier/v2 ever stops being maintained or develops a serious bug we can't work around, `BurntSushi/toml` remains the swap-in fallback. The migration surface is roughly `internal/config/`'s loader.
