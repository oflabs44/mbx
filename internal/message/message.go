// Package message is the full-content view of mail: body, attachments,
// optional raw MIME parts. The cheap "envelope" view lives in
// internal/envelope and the two share an mbx ID via internal/mbxid.
//
// Backends in internal/provider/* satisfy small consumer interfaces
// (Reader, RawReader) defined alongside their consumers here. The body-
// extraction policy (plain-first, html-as-text fallback) lives in
// body.go — providers feed candidates, domain picks the surface body.
package message

import (
	"time"

	"github.com/oflabs44/mbx/internal/attachment"
)

// Message is the JSON shape `mbx message read` emits. Field names are
// part of the documented output contract — see docs/commands.md.
type Message struct {
	ID          string            `json:"id"`
	ThreadID    string            `json:"thread_id,omitempty"`
	Account     string            `json:"account"`
	From        string            `json:"from,omitempty"`
	To          []string          `json:"to,omitempty"`
	Cc          []string          `json:"cc,omitempty"`
	Subject     string            `json:"subject,omitempty"`
	Date        time.Time         `json:"date,omitzero"`
	Body        string            `json:"body,omitempty"`
	BodySource  string            `json:"body_source,omitempty"`
	Parts       []Part            `json:"parts,omitempty"`
	Attachments []attachment.Meta `json:"attachments"`
	Headers     map[string]string `json:"headers,omitempty"`
	Provider    string            `json:"provider"`
}

// Part is the --raw view: the message's MIME tree as a flat list of
// leaf parts (no nesting). Each part's Body is already decoded to UTF-8
// where possible.
type Part struct {
	MIME     string            `json:"mime"`
	Filename string            `json:"filename,omitempty"`
	Body     string            `json:"body,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}
