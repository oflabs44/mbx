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

// Archiver is the narrow consumer interface for `message archive`. The
// returned dest is the resolved archive folder where it exists (IMAP)
// and empty where it doesn't (Gmail, which archives by removing INBOX
// rather than moving to a folder). See ADR-0009.
type Archiver interface {
	ArchiveMessages(ctx context.Context, ids []mbxid.ID) (newIDs []mbxid.ID, dest string, err error)
}

// Move, Copy, Delete, and Archive are the domain entry points. Currently
// thin pass-throughs; kept for parity with the read-path verbs so future
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

func Archive(ctx context.Context, a Archiver, ids []mbxid.ID) (newIDs []mbxid.ID, dest string, err error) {
	return a.ArchiveMessages(ctx, ids)
}
