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
	CodeProviderUnsupported   Code = "provider.unsupported"

	CodeCacheUnavailable    Code = "cache.unavailable"
	CodeCacheSchemaMismatch Code = "cache.schema_mismatch"

	CodeConfigInvalid        Code = "config.invalid"
	CodeConfigUnknownAccount Code = "config.unknown_account"

	CodeFanoutAllFailed Code = "fanout.all_failed"

	CodeGeneric Code = "generic"
)

// ExitCode maps a Code to its documented process exit code. The class is
// determined by prefix; specific codes within a class get distinct numbers
// per the table in docs/commands.md (e.g. auth.missing_write_cmd → 11).
// Unmapped codes within a known class fall through to the class baseline.
func ExitCode(c Code) int {
	switch c {
	case CodeOK:
		return 0
	case CodeAuthRefreshFailed:
		return 10
	case CodeAuthMissingWriteCmd:
		return 11
	case CodeAuthInvalid:
		return 12
	case CodeProviderRateLimit:
		return 20
	case CodeProviderNotFound:
		return 21
	case CodeProviderIDInvalidated:
		return 22
	case CodeProviderTimeout:
		return 23
	case CodeProviderUnsupported:
		return 24
	case CodeCacheUnavailable:
		return 30
	case CodeCacheSchemaMismatch:
		return 31
	case CodeConfigInvalid:
		return 40
	case CodeConfigUnknownAccount:
		return 41
	case CodeFanoutAllFailed:
		return 50
	}
	switch {
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
	case strings.HasPrefix(string(c), "fanout."):
		return 50
	default:
		return 1
	}
}
