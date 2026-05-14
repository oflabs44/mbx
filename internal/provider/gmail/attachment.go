package gmail

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"

	"github.com/oflabs44/mbx/internal/attachment"
	"github.com/oflabs44/mbx/internal/mbxid"
)

// ListAttachments satisfies attachment.Lister: messages.get with format=full,
// then walk the MIME tree using the same walker the message read path uses.
// Body candidates are discarded — we only need the attachment slice.
func (c *Client) ListAttachments(ctx context.Context, msgID mbxid.ID) ([]attachment.Meta, error) {
	if err := c.assertOwns(msgID); err != nil {
		return nil, err
	}
	m, err := c.svc.Users.Messages.Get("me", msgID.GmailMsgID).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, mapErr(err)
	}
	_, atts, _ := walkParts(m.Payload)
	return attachAttachmentIDs(c.Account, m.Id, atts), nil
}

// DownloadAttachment satisfies attachment.Downloader. Re-fetches the
// message at format=full to resolve index → Gmail attachmentId, then
// pulls the actual bytes via attachments.get. The double round-trip
// is the cost of keeping the mbx attachment ID self-describing without
// embedding Gmail's opaque attachmentId in it.
func (c *Client) DownloadAttachment(ctx context.Context, msgID mbxid.ID, index int) (attachment.Data, error) {
	if err := c.assertOwns(msgID); err != nil {
		return attachment.Data{}, err
	}

	m, err := c.svc.Users.Messages.Get("me", msgID.GmailMsgID).Format("full").Context(ctx).Do()
	if err != nil {
		return attachment.Data{}, mapErr(err)
	}
	_, atts, _ := walkParts(m.Payload)
	if index < 0 || index >= len(atts) {
		return attachment.Data{}, fmt.Errorf("gmail: attachment index %d out of range (message has %d)", index, len(atts))
	}
	target := atts[index]

	body, err := c.svc.Users.Messages.Attachments.Get("me", msgID.GmailMsgID, target.GmailID).Context(ctx).Do()
	if err != nil {
		return attachment.Data{}, mapErr(err)
	}
	raw, err := base64.URLEncoding.DecodeString(body.Data)
	if err != nil {
		return attachment.Data{}, fmt.Errorf("gmail: decode attachment %d: %w", index, err)
	}
	return attachment.Data{
		Filename: target.Filename,
		MIME:     target.MIME,
		Bytes:    raw,
	}, nil
}

// assertOwns rejects mbx IDs that aren't gmail or that belong to a
// different account on this client. An ID minted under one of the
// account's aliases (ADR-0007) is accepted — same account, prior name.
func (c *Client) assertOwns(id mbxid.ID) error {
	if id.Provider != mbxid.Gmail {
		return fmt.Errorf("gmail: id %q is not a gmail id", id.String())
	}
	if id.Account == c.Account || slices.Contains(c.Aliases, id.Account) {
		return nil
	}
	return fmt.Errorf("gmail: id account %q does not match client %q", id.Account, c.Account)
}
