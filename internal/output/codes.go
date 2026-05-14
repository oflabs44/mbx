package output

import "strings"

// Stable identifier; semantics never change without bumping the envelope
// Version. Canonical taxonomy: docs/commands.md.
type Code string

const (
	CodeOK Code = ""

	CodeUsageInvalid     Code = "usage.invalid"
	CodeInputMissingFlag Code = "input.missing_flag"
	CodeInputAmbiguous   Code = "input.ambiguous_body"

	CodeAuthRefreshFailed   Code = "auth.refresh_failed"
	CodeAuthMissingWriteCmd Code = "auth.missing_write_cmd"
	CodeAuthInvalid         Code = "auth.invalid_credentials"

	CodeProviderRateLimit     Code = "provider.rate_limited"
	CodeProviderNotFound      Code = "provider.not_found"
	CodeProviderIDInvalidated Code = "provider.id_invalidated"
	CodeProviderTimeout       Code = "provider.network_timeout"

	CodeCacheUnavailable    Code = "cache.unavailable"
	CodeCacheSchemaMismatch Code = "cache.schema_mismatch"

	CodeConfigInvalid        Code = "config.invalid"
	CodeConfigUnknownAccount Code = "config.unknown_account"

	CodeGeneric Code = "generic"
)

// Prefix-based mapping documented in docs/commands.md.
func ExitCode(c Code) int {
	switch {
	case c == CodeOK:
		return 0
	case strings.HasPrefix(string(c), "usage."), strings.HasPrefix(string(c), "input."):
		return 2
	case strings.HasPrefix(string(c), "auth."):
		return 10
	case strings.HasPrefix(string(c), "provider."):
		return 20
	case strings.HasPrefix(string(c), "cache."):
		return 30
	case strings.HasPrefix(string(c), "config."):
		return 40
	default:
		return 1
	}
}
