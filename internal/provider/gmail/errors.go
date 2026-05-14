// Package gmail is the Gmail HTTP API backend. It satisfies the narrow
// consumer interfaces defined in internal/envelope, internal/message,
// internal/folder, and internal/attachment. The interface names live with
// the consumers; this package never imports them for type declarations.
package gmail

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/api/googleapi"

	"github.com/oflabs44/mbx/internal/output"
)

// mapErr translates a googleapi.Error into a stable mbx output.Failure.
// Non-googleapi errors are wrapped with a "gmail:" prefix and bubbled
// as-is — they hit the generic error path in main.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return fmt.Errorf("gmail: %w", err)
	}
	switch {
	case gerr.Code == 401:
		return output.Errorf(output.CodeAuthRefreshFailed,
			"Gmail rejected the OAuth token: %s", gerr.Message)
	case gerr.Code == 403 && strings.Contains(strings.ToLower(gerr.Message), "quota"):
		return output.Errorf(output.CodeProviderRateLimit,
			"Gmail quota exceeded: %s", gerr.Message)
	case gerr.Code == 404:
		return output.Errorf(output.CodeProviderNotFound,
			"Gmail: %s", gerr.Message)
	case gerr.Code == 429:
		return output.Errorf(output.CodeProviderRateLimit,
			"Gmail rate limit hit: %s", gerr.Message)
	case gerr.Code >= 500:
		return output.Errorf(output.CodeProviderTimeout,
			"Gmail upstream error (%d): %s", gerr.Code, gerr.Message)
	}
	return output.Errorf(output.CodeGeneric,
		"gmail (%d): %s", gerr.Code, gerr.Message)
}
