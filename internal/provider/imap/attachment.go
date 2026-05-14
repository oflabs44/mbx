package imap

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	gomsg "github.com/emersion/go-message"

	"github.com/oflabs44/mbx/internal/attachment"
	"github.com/oflabs44/mbx/internal/mbxid"
)

// ListAttachments satisfies attachment.Lister. Re-uses the same MIME
// walk the message reader does — discards body candidates, returns only
// the attachment slice (stamped with mbx IDs).
func (c *Client) ListAttachments(ctx context.Context, msgID mbxid.ID) ([]attachment.Meta, error) {
	if err := c.assertOwns(msgID); err != nil {
		return nil, err
	}
	if err := c.selectAndVerify(msgID); err != nil {
		return nil, err
	}
	raw, err := c.fetchBody(msgID.UID)
	if err != nil {
		return nil, err
	}
	ent, err := gomsg.Read(bytes.NewReader(raw))
	if err != nil && !gomsg.IsUnknownCharset(err) && !gomsg.IsUnknownEncoding(err) {
		return nil, fmt.Errorf("imap: parse for attachments %s: %w", msgID.String(), err)
	}
	_, atts, _ := walkEntity(ent)
	return attachment.Stamp(msgID, atts), nil
}

// DownloadAttachment satisfies attachment.Downloader. Single MIME walk:
// the predicate matches walkEntity's exactly (skip multipart containers,
// require filename or attachment Content-Disposition), capturing bytes
// for the indexed part as we go. Avoids the index-drift hazard of
// running two parallel walks.
func (c *Client) DownloadAttachment(ctx context.Context, msgID mbxid.ID, index int) (attachment.Data, error) {
	if err := c.assertOwns(msgID); err != nil {
		return attachment.Data{}, err
	}
	if err := c.selectAndVerify(msgID); err != nil {
		return attachment.Data{}, err
	}
	raw, err := c.fetchBody(msgID.UID)
	if err != nil {
		return attachment.Data{}, err
	}
	ent, err := gomsg.Read(bytes.NewReader(raw))
	if err != nil && !gomsg.IsUnknownCharset(err) && !gomsg.IsUnknownEncoding(err) {
		return attachment.Data{}, fmt.Errorf("imap: parse for attachment download %s: %w", msgID.String(), err)
	}

	var (
		captured *attachment.Data
		seen     int
	)
	_ = ent.Walk(func(_ []int, e *gomsg.Entity, walkErr error) error {
		if walkErr != nil && !gomsg.IsUnknownCharset(walkErr) && !gomsg.IsUnknownEncoding(walkErr) {
			return walkErr
		}
		mt, _, _ := e.Header.ContentType()
		mt = strings.ToLower(mt)
		if strings.HasPrefix(mt, "multipart/") {
			return nil
		}
		fn := contentDispositionFilename(e.Header)
		if fn == "" && !isAttachmentDisposition(e.Header) {
			return nil
		}
		if seen == index {
			captured = &attachment.Data{
				Filename: fn,
				MIME:     mt,
				Bytes:    []byte(readAll(e.Body)),
			}
		}
		seen++
		return nil
	})
	if captured == nil {
		return attachment.Data{}, fmt.Errorf("imap: attachment index %d out of range (message has %d)", index, seen)
	}
	return *captured, nil
}
