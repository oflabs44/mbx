// Package attachment is the listing-and-download surface for message
// attachments. Cheap metadata (filename, size, MIME) lives behind the
// Lister interface; bytes flow through Downloader.
//
// The mbx attachment ID format is the message's mbx ID with a trailing
// ":att-<index>" suffix — see internal/mbxid for the parent format and
// SplitID below for the suffix split. The index is sequential across
// the message's MIME tree (depth-first, document order).
package attachment

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/oflabs44/mbx/internal/mbxid"
)

// Meta is the JSON shape `mbx attachment list` (and message read's
// attachments[]) emit. ID is the self-describing attachment ID.
type Meta struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MIME     string `json:"mime"`
}

// Data is what Downloader returns: the bytes plus enough metadata to
// pick a sane output filename when the user passed a directory.
type Data struct {
	Filename string
	MIME     string
	Bytes    []byte
}

// Lister is the only capability `attachment list` needs from a backend.
type Lister interface {
	ListAttachments(ctx context.Context, msgID mbxid.ID) ([]Meta, error)
}

// Downloader is the bytes-fetching capability `attachment download` needs.
// Index is the part position within the message's MIME tree, matching
// the suffix on the mbx attachment ID.
type Downloader interface {
	DownloadAttachment(ctx context.Context, msgID mbxid.ID, index int) (Data, error)
}

// SplitID parses an mbx attachment ID into its message-ID and part-index
// components. The wire format is "<msg-id>:att-<digits>". Returns the
// parsed message ID and the integer index, or an error if either piece
// is malformed.
func SplitID(s string) (mbxid.ID, int, error) {
	idx := strings.LastIndex(s, ":att-")
	if idx < 0 {
		return mbxid.ID{}, 0, fmt.Errorf("attachment id missing ':att-<n>' suffix: %q", s)
	}
	msgPart, suffix := s[:idx], s[idx+len(":att-"):]
	n, err := strconv.Atoi(suffix)
	if err != nil {
		return mbxid.ID{}, 0, fmt.Errorf("attachment id index not an integer: %q", suffix)
	}
	if n < 0 {
		return mbxid.ID{}, 0, fmt.Errorf("attachment id index must be non-negative: %d", n)
	}
	id, err := mbxid.Parse(msgPart)
	if err != nil {
		return mbxid.ID{}, 0, fmt.Errorf("attachment id message portion: %w", err)
	}
	return id, n, nil
}

// FormatID is the inverse of SplitID. Used by backends when stamping
// Meta.ID at list time.
func FormatID(msgID mbxid.ID, index int) string {
	return fmt.Sprintf("%s:att-%d", msgID.String(), index)
}
