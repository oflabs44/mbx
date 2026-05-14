// Package folder is the listing-shaped view of containers an Envelope
// can belong to. Cross-provider vocabulary; for Gmail "folder" =
// non-flag-mapped label, for IMAP "folder" = mailbox.
//
// The narrow consumer interface (Lister) lives here; backends in
// internal/provider/* implement matching methods.
package folder

import "context"

// Folder is the JSON shape `mbx folder list` emits. Field names are
// part of the documented output contract — see docs/commands.md.
//
// Provider-extras live under namespaced subobjects (Gmail) so the top
// level stays normalized.
type Folder struct {
	Name   string `json:"name"`
	Count  int64  `json:"count"`
	Unread int64  `json:"unread"`

	Gmail *GmailFolderExtras `json:"gmail,omitempty"`
}

// GmailFolderExtras carries Gmail-specific counts callers occasionally
// need without forcing them into the normalized top level. ThreadsTotal
// reflects what users see in Gmail's UI, while top-level Count tracks
// individual messages (matches IMAP semantics).
type GmailFolderExtras struct {
	ThreadsTotal int64 `json:"threads_total"`
}

// Lister is the only capability `folder list` requires from a backend.
// Defined here, next to its sole consumer.
type Lister interface {
	ListFolders(ctx context.Context) ([]Folder, error)
}
