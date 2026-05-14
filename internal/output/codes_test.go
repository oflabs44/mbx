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
		{CodeAuthMissingWriteCmd, 10},
		{CodeAuthInvalid, 10},
		{CodeProviderRateLimit, 20},
		{CodeProviderNotFound, 20},
		{CodeProviderIDInvalidated, 20},
		{CodeProviderTimeout, 20},
		{CodeCacheUnavailable, 30},
		{CodeCacheSchemaMismatch, 30},
		{CodeConfigInvalid, 40},
		{CodeConfigUnknownAccount, 40},
		{CodeGeneric, 1},
		{"unknown.thing", 1},
	}
	for _, c := range cases {
		t.Run(string(c.code), func(t *testing.T) {
			if got := ExitCode(c.code); got != c.want {
				t.Errorf("ExitCode(%q) = %d, want %d", c.code, got, c.want)
			}
		})
	}
}
