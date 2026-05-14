package output

import "testing"

func TestExitCode(t *testing.T) {
	cases := []struct {
		code Code
		want int
	}{
		{CodeOK, 0},
		{CodeUsageInvalid, 2},
		{CodeInputMissingFlag, 2},
		{CodeInputAmbiguous, 2},
		{CodeAuthRefreshFailed, 10},
		{CodeAuthMissingWriteCmd, 11},
		{CodeAuthInvalid, 12},
		{CodeProviderRateLimit, 20},
		{CodeProviderNotFound, 21},
		{CodeProviderIDInvalidated, 22},
		{CodeProviderTimeout, 23},
		{CodeProviderUnsupported, 24},
		{CodeCacheUnavailable, 30},
		{CodeCacheSchemaMismatch, 31},
		{CodeConfigInvalid, 40},
		{CodeConfigUnknownAccount, 41},
		{CodeGeneric, 1},
		{"unknown.thing", 1},
		{"auth.something_new", 10},
		{"provider.future", 20},
		{"config.future", 40},
	}
	for _, c := range cases {
		t.Run(string(c.code), func(t *testing.T) {
			if got := ExitCode(c.code); got != c.want {
				t.Errorf("ExitCode(%q) = %d, want %d", c.code, got, c.want)
			}
		})
	}
}
