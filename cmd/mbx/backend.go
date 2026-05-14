package main

import (
	"context"

	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/output"
	"github.com/oflabs44/mbx/internal/provider/gmail"
)

// newBackend returns the constructed provider client for an account.
// Returning the concrete type lets handlers use it as whatever narrow
// interface they need (Lister, Reader, RawReader, Downloader, ...).
//
// IMAP support lands in phase 3. When that arrives this function will
// need a small union type because two concrete returns can't share a
// signature; for now, one backend means one helper.
func newBackend(ctx context.Context, name string, acct *config.Account) (*gmail.Client, error) {
	switch acct.Backend.Type {
	case config.BackendGmail:
		return gmail.New(ctx, name, acct)
	default:
		return nil, output.Errorf(output.CodeUsageInvalid,
			"%s backend support lands in phase 3 (account %q is type %q)",
			acct.Backend.Type, name, acct.Backend.Type)
	}
}
