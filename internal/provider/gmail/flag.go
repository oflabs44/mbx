package gmail

import (
	"context"

	gmailv1 "google.golang.org/api/gmail/v1"

	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/output"
)

// FlagEnvelopes satisfies envelope.Flagger.
//
// Gmail represents state via labels, not IMAP flags. The mbx vocabulary
// maps to labels asymmetrically:
//
//   - seen     ⇄ inverse of the UNREAD label
//   - flagged  ⇄ STARRED label
//
// answered/draft/deleted have no API-settable Gmail equivalent: \Draft
// is implicit in the drafts API, \Deleted has no analogue (trash/delete
// live on `message delete` instead), \Answered is not surfaced. We reject
// those with provider.unsupported rather than silently dropping them.
//
// Multi-ID input is fail-fast: the loop returns on the first failed
// modify call, leaving earlier IDs already mutated server-side. The
// supported label diffs (UNREAD/STARRED add/remove) are idempotent, so
// retrying the same command after a transient failure is safe.
func (c *Client) FlagEnvelopes(ctx context.Context, ids []mbxid.ID, add, remove []envelope.Flag) error {
	if err := c.assertAllOwn(ids); err != nil {
		return err
	}
	addLabels, removeLabels, err := computeLabelDiff(add, remove)
	if err != nil {
		return err
	}
	if len(addLabels) == 0 && len(removeLabels) == 0 {
		return nil
	}
	req := &gmailv1.ModifyMessageRequest{AddLabelIds: addLabels, RemoveLabelIds: removeLabels}
	for _, id := range ids {
		if _, err := c.svc.Users.Messages.Modify("me", id.GmailMsgID, req).Context(ctx).Do(); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// computeLabelDiff translates an mbx flag delta into a (addLabelIds,
// removeLabelIds) pair for users.messages.modify. Adding `seen` removes
// UNREAD and vice versa — the positive/negative form mismatch is handled
// here, not in the call site.
func computeLabelDiff(add, remove []envelope.Flag) (addLabels, removeLabels []string, err error) {
	for _, f := range add {
		switch f {
		case envelope.FlagSeen:
			removeLabels = append(removeLabels, "UNREAD")
		case envelope.FlagFlagged:
			addLabels = append(addLabels, "STARRED")
		default:
			return nil, nil, output.Errorf(output.CodeProviderUnsupported,
				"gmail: cannot add flag %q via envelope flag (only seen and flagged are settable)", f)
		}
	}
	for _, f := range remove {
		switch f {
		case envelope.FlagSeen:
			addLabels = append(addLabels, "UNREAD")
		case envelope.FlagFlagged:
			removeLabels = append(removeLabels, "STARRED")
		default:
			return nil, nil, output.Errorf(output.CodeProviderUnsupported,
				"gmail: cannot remove flag %q via envelope flag (only seen and flagged are settable)", f)
		}
	}
	return addLabels, removeLabels, nil
}
