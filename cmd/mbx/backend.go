package main

import (
	"context"

	"github.com/oflabs44/mbx/internal/attachment"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/folder"
	"github.com/oflabs44/mbx/internal/message"
	"github.com/oflabs44/mbx/internal/output"
	"github.com/oflabs44/mbx/internal/provider/gmail"
	"github.com/oflabs44/mbx/internal/provider/imap"
	"github.com/oflabs44/mbx/internal/provider/smtp"
)

// backend is the cmd/mbx-level union of every consumer-side capability
// interface a phase-2/3 read-path command may need. Defined here, not in
// internal/provider/*, because it exists to give cmd handlers a single
// concrete return from newBackend — providers themselves should never
// import this; they satisfy the narrow interfaces structurally.
type backend interface {
	envelope.Lister
	message.Reader
	message.RawReader
	folder.Lister
	attachment.Lister
	attachment.Downloader
}

// newBackend returns the constructed provider client for an account as
// the union interface above. Every backend type returned must satisfy
// every method on `backend` — that's the cost of one helper for all
// commands. If a future provider can't (e.g. a send-only transport),
// it doesn't go through newBackend.
//
// IMAP clients hold a live TCP connection; callers should defer Close()
// on the returned value when the underlying type exposes one. Today,
// only *imap.Client does — *gmail.Client uses stateless HTTP and needs
// no teardown.
func newBackend(ctx context.Context, name string, acct *config.Account) (backend, error) {
	switch acct.Backend.Type {
	case config.BackendGmail:
		return gmail.New(ctx, name, acct)
	case config.BackendIMAP:
		return imap.New(ctx, name, acct)
	default:
		return nil, output.Errorf(output.CodeUsageInvalid,
			"backend type %q is not supported (account %q)",
			acct.Backend.Type, name)
	}
}

// closeBackend issues a connection-level teardown for backends that
// hold open resources (IMAP/SMTP TCP connections). Safe to call on any
// value; no-op for types that don't expose Close(). Accepts `any` so
// both read-path (backend) and send-path (message.Sender) callers share
// it — the actual close happens via type assertion.
func closeBackend(v any) {
	if c, ok := v.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

// newSendBackend is a separate seam from the read-path newBackend:
// Gmail accounts send via the Gmail HTTP API (same gmail.Client), but
// IMAP accounts send via a parallel SMTP connection rather than the
// IMAP one. Returning the message.Sender interface keeps callers free
// of provider-specific types. Callers defer closeBackend on the result.
func newSendBackend(ctx context.Context, name string, acct *config.Account) (message.Sender, error) {
	switch acct.Backend.Type {
	case config.BackendGmail:
		return gmail.New(ctx, name, acct)
	case config.BackendIMAP:
		return smtp.New(ctx, name, acct)
	default:
		return nil, output.Errorf(output.CodeUsageInvalid,
			"backend type %q is not supported (account %q)",
			acct.Backend.Type, name)
	}
}
