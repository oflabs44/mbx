package gmail

import (
	"context"

	gmailv1 "google.golang.org/api/gmail/v1"

	"github.com/oflabs44/mbx/internal/mbxid"
)

// MoveMessages satisfies message.Mover. Gmail has no native "move" — a
// message lives once and appears in folders by virtue of its label set.
// "Move to X" in the Gmail UI archives from INBOX and adds the X label;
// we mirror that: addLabelIds=[dest], removeLabelIds=[INBOX]. If the
// source message is not in INBOX, the remove side is a server-side no-op.
//
// The mbx ID does not change across a Gmail move (the message ID is
// stable; only labels move), so the returned slice mirrors the input.
//
// Multi-ID input is fail-fast; idempotent at the API level (re-applying
// the same label diff is a no-op), so retry-after-failure is safe.
func (c *Client) MoveMessages(ctx context.Context, ids []mbxid.ID, dest string) ([]mbxid.ID, error) {
	return ids, c.modifyAll(ctx, ids, &gmailv1.ModifyMessageRequest{
		AddLabelIds:    []string{dest},
		RemoveLabelIds: []string{"INBOX"},
	})
}

// CopyMessages satisfies message.Copier. For Gmail this is "add the dest
// label" — the message still exists once but is visible in two folders.
// The mbx ID is unchanged; we return the input ids verbatim.
func (c *Client) CopyMessages(ctx context.Context, ids []mbxid.ID, dest string) ([]mbxid.ID, error) {
	return ids, c.modifyAll(ctx, ids, &gmailv1.ModifyMessageRequest{AddLabelIds: []string{dest}})
}

// DeleteMessages satisfies message.Deleter. Default routes through
// users.messages.trash (recoverable from Trash); --permanent uses
// users.messages.delete (irrecoverable, requires the
// gmail.modify-or-stronger scope which mbx already requests).
//
// Multi-ID input is fail-fast. Default delete (trash) is idempotent —
// re-running over the same ids is safe. --permanent is NOT: a retry
// after partial failure will 404 on the IDs already deleted. Re-list
// the surviving ids before retrying.
func (c *Client) DeleteMessages(ctx context.Context, ids []mbxid.ID, permanent bool) error {
	if err := c.assertAllOwn(ids); err != nil {
		return err
	}
	for _, id := range ids {
		var err error
		if permanent {
			err = c.svc.Users.Messages.Delete("me", id.GmailMsgID).Context(ctx).Do()
		} else {
			_, err = c.svc.Users.Messages.Trash("me", id.GmailMsgID).Context(ctx).Do()
		}
		if err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// modifyAll applies the same Modify request to every id. Used by Move
// and Copy (one label diff per verb) — Delete uses a different endpoint
// (trash/delete) and doesn't share this loop.
func (c *Client) modifyAll(ctx context.Context, ids []mbxid.ID, req *gmailv1.ModifyMessageRequest) error {
	if err := c.assertAllOwn(ids); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := c.svc.Users.Messages.Modify("me", id.GmailMsgID, req).Context(ctx).Do(); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// assertAllOwn loops assertOwns over a slice. Shared across the mutate
// and flag verbs so each doesn't open-code the same preflight.
func (c *Client) assertAllOwn(ids []mbxid.ID) error {
	for _, id := range ids {
		if err := c.assertOwns(id); err != nil {
			return err
		}
	}
	return nil
}
