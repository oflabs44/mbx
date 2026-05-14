// Package envelope is the cheap, listing-shaped view of a piece of mail:
// id, headers, flags, snippet. Bodies and attachments live in
// internal/message and internal/attachment. The envelope-vs-message split
// is load-bearing — see CONTEXT.md.
//
// Backend capabilities are expressed as small consumer-defined interfaces
// (Lister, ThreadSearcher, Flagger). Backends in internal/provider/* satisfy
// the interfaces by writing matching methods; they never import the
// interface definitions for declaration purposes (Go interface idiom).
package envelope

import "time"

// Envelope is the JSON shape `mbx envelope list` and `search` emit. Field
// names are part of the documented output contract — see docs/commands.md.
//
// Provider-specific extras live under a namespaced subobject (Gmail) so the
// top level stays normalized and cross-provider stable.
type Envelope struct {
	ID            string    `json:"id"`
	ThreadID      string    `json:"thread_id,omitempty"`
	Account       string    `json:"account"`
	From          string    `json:"from,omitempty"`
	To            []string  `json:"to,omitempty"`
	Cc            []string  `json:"cc,omitempty"`
	Subject       string    `json:"subject,omitempty"`
	Date          time.Time `json:"date,omitzero"`
	Flags         []string  `json:"flags"`
	Folders       []string  `json:"folders"`
	Snippet       string    `json:"snippet,omitempty"`
	HasAttachment bool      `json:"has_attachment"`
	Provider      string    `json:"provider"`

	Gmail *GmailExtras `json:"gmail,omitempty"`
}

// GmailExtras carries provider-native fields callers occasionally need
// without forcing them into the normalized top level.
type GmailExtras struct {
	Labels []string `json:"labels,omitempty"`
}
