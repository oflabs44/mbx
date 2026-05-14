package message

import (
	"context"

	"github.com/oflabs44/mbx/internal/mbxid"
)

// Mover, Copier, and Deleter are the narrow consumer interfaces for the
// three write-path message verbs. Defined here, next to the cmd handlers
// that depend on them; backends in internal/provider/* satisfy by writing
// methods with matching signatures.
//
// Move and Copy return one new ID per input ID, in input order. For Gmail
// the returned IDs equal the input IDs (Gmail messages keep their id
// across label changes); for IMAP each returned ID encodes the dest
// folder and its newly-assigned UID.
type Mover interface {
	MoveMessages(ctx context.Context, ids []mbxid.ID, dest string) ([]mbxid.ID, error)
}

type Copier interface {
	CopyMessages(ctx context.Context, ids []mbxid.ID, dest string) ([]mbxid.ID, error)
}

// Deleter has two flavours of behaviour gated by `permanent`. Default
// (`permanent=false`) is "move to trash"; permanent skips trash and
// hard-deletes. The backend resolves "trash" — for IMAP via
// folder.aliases.trash, for Gmail via the system Trash label.
type Deleter interface {
	DeleteMessages(ctx context.Context, ids []mbxid.ID, permanent bool) error
}

// Move, Copy, and Delete are the domain entry points. Currently thin
// pass-throughs; kept for parity with the read-path verbs so future
// cross-backend bounds (per-call timeouts, batch caps) land in one place.
func Move(ctx context.Context, m Mover, ids []mbxid.ID, dest string) ([]mbxid.ID, error) {
	return m.MoveMessages(ctx, ids, dest)
}

func Copy(ctx context.Context, c Copier, ids []mbxid.ID, dest string) ([]mbxid.ID, error) {
	return c.CopyMessages(ctx, ids, dest)
}

func Delete(ctx context.Context, d Deleter, ids []mbxid.ID, permanent bool) error {
	return d.DeleteMessages(ctx, ids, permanent)
}
