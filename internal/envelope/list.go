package envelope

import (
	"context"
	"time"
)

// Lister is the only capability List requires from a backend. Defined here,
// next to its sole consumer; not in internal/provider. A backend satisfies
// it by writing a method with the matching signature.
type Lister interface {
	ListEnvelopes(ctx context.Context, q ListQuery) (Page, error)
}

// ListQuery is the normalized request a backend's ListEnvelopes receives.
// Pointer fields (Unread, Starred, HasAttachment) are tri-state: nil means
// "no filter", non-nil means "filter to this value".
type ListQuery struct {
	Folder        string
	Limit         int
	Cursor        string
	Unread        *bool
	Starred       *bool
	HasAttachment *bool
	From          string
	To            string
	After         time.Time
	Before        time.Time
	RawQuery      string
}

// Page is what a Lister returns: the envelopes for this page plus an opaque
// cursor for the next. Empty NextCursor means there are no more pages.
type Page struct {
	Envelopes  []Envelope
	NextCursor string
}

// DefaultLimit caps an unset Limit. Matches the per-account default
// documented in commands.md.
const DefaultLimit = 20

// MaxLimit caps an oversized request. Gmail's messages.list maxResults
// tops out at 500; IMAP has no upper bound but we hold to the same number
// for consistency.
const MaxLimit = 500

// List forwards to the backend after applying defaults and bounds. The
// domain layer is the single place these knobs live.
func List(ctx context.Context, b Lister, q ListQuery) (Page, error) {
	if q.Limit <= 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit > MaxLimit {
		q.Limit = MaxLimit
	}
	return b.ListEnvelopes(ctx, q)
}
