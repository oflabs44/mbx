package gmail

import (
	"context"
	"fmt"

	gmailv1 "google.golang.org/api/gmail/v1"

	"github.com/oflabs44/mbx/internal/output"
)

// AddFolder satisfies folder.Adder. Gmail labels are mbx folders; create
// a user-visible (LabelListVisibility=labelShow) message label. Reserved
// system label names (INBOX, SENT, etc.) are server-rejected and surface
// as the standard provider error.
func (c *Client) AddFolder(ctx context.Context, name string) error {
	lbl := &gmailv1.Label{
		Name:                  name,
		LabelListVisibility:   "labelShow",
		MessageListVisibility: "show",
	}
	_, err := c.svc.Users.Labels.Create("me", lbl).Context(ctx).Do()
	return mapErr(err)
}

// DeleteFolder satisfies folder.Deleter. Gmail labels are not message
// containers: deleting a label simply removes the label from every
// message that carries it (the messages themselves stay). So the
// "non-empty fails without --force" rule documented for IMAP doesn't
// apply here; --force is accepted but a no-op on Gmail.
func (c *Client) DeleteFolder(ctx context.Context, name string, _ bool) error {
	id, err := c.labelIDByName(ctx, name)
	if err != nil {
		return err
	}
	return mapErr(c.svc.Users.Labels.Delete("me", id).Context(ctx).Do())
}

// ExpungeFolder satisfies folder.Expunger. Gmail has no manual expunge:
// items in Trash auto-purge after 30 days, and no other folder retains
// soft-deleted messages. Returning nil for the operation keeps `mbx
// folder expunge` safe to run unconditionally in scripts that span
// Gmail and IMAP accounts.
//
// We still resolve the label first — a typo'd folder name on Gmail
// would otherwise report silent success. Parity with the other Gmail
// verbs: the name must exist before the verb completes.
func (c *Client) ExpungeFolder(ctx context.Context, name string) error {
	if _, err := c.labelIDByName(ctx, name); err != nil {
		return err
	}
	return nil
}

// PurgeFolder satisfies folder.Purger. "Delete all messages in the
// folder" maps to: list every message carrying the label, then hard-
// delete each via users.messages.delete. This is the destructive verb
// (irrecoverable, no Trash hop) — the cmd layer gates it on --yes.
//
// SURPRISE worth knowing: Gmail messages can carry multiple labels.
// Purging "Receipts" hard-deletes every message also in INBOX (or any
// other folder) that happened to be tagged Receipts. This matches a
// literal reading of "delete every message in the folder," but is
// rarely what users want for non-leaf labels. Documented in
// docs/commands.md so the destructive scope is explicit.
//
// Pagination loops until exhausted; failures inside the inner delete
// loop bubble up fail-fast. The wrapped error names the count of
// already-deleted messages so the operator can reason about partial
// progress. Re-running is safe (already-deleted ids 404 cleanly).
func (c *Client) PurgeFolder(ctx context.Context, name string) error {
	id, err := c.labelIDByName(ctx, name)
	if err != nil {
		return err
	}
	deleted := 0
	var pageToken string
	for {
		call := c.svc.Users.Messages.List("me").LabelIds(id).MaxResults(500)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Context(ctx).Do()
		if err != nil {
			return mapErr(err)
		}
		for _, m := range resp.Messages {
			if err := c.svc.Users.Messages.Delete("me", m.Id).Context(ctx).Do(); err != nil {
				return fmt.Errorf("gmail: purge %q failed after deleting %d messages, on message %s: %w",
					name, deleted, m.Id, mapErr(err))
			}
			deleted++
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return nil
}

// labelIDByName resolves a label name to its Gmail label ID. mbx
// surfaces labels as folders by name, but the labels API addresses by
// opaque ID — so every label-targeted mutation translates here first.
// Missing names surface as provider.not_found.
func (c *Client) labelIDByName(ctx context.Context, name string) (string, error) {
	resp, err := c.svc.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		return "", mapErr(err)
	}

	return resolveLabelID(resp.Labels, name)
}

// resolveLabelID is the pure lookup behind labelIDByName. Split out so
// the matching rule (exact-name, system-labels-resolve-as-themselves)
// can be exercised without standing up a Gmail service.
func resolveLabelID(labels []*gmailv1.Label, name string) (string, error) {
	for _, l := range labels {
		if l.Name == name {
			return l.Id, nil
		}
	}

	return "", output.Errorf(output.CodeProviderNotFound,
		"gmail: label %q not found", name).WithDetails("name", name)
}
