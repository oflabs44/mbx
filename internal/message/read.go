package message

import (
	"context"

	"github.com/oflabs44/mbx/internal/mbxid"
)

// Reader is the only capability `message read` requires from a backend.
// Defined here, not in internal/provider — interfaces live with the
// consumer.
type Reader interface {
	ReadMessage(ctx context.Context, id mbxid.ID, opt ReadOptions) (Message, error)
}

// RawReader is the raw-bytes view used by `message export`. Separate
// interface because the body-shape (raw RFC 5322 bytes) and the use
// (binary stdout, no JSON) differ enough that smushing them onto Reader
// would force a conditional shape in every implementation.
type RawReader interface {
	ReadMessageRaw(ctx context.Context, id mbxid.ID) ([]byte, error)
}

// ReadOptions captures the per-call knobs from `message read`'s flags.
// PreferHTML / IncludeRaw are mutually exclusive; the handler enforces
// that before constructing this struct.
type ReadOptions struct {
	PreferHTML     bool
	IncludeRaw     bool
	IncludeHeaders []string
	OmitHeaders    bool
	MarkSeen       bool
}
