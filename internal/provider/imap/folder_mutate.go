package imap

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"
)

// AddFolder satisfies folder.Adder. CREATE on the IMAP server. Folder
// hierarchies are server-defined (most use "/" or "."); mbx passes the
// name through verbatim.
func (c *Client) AddFolder(_ context.Context, name string) error {
	if err := c.c.Create(name, nil).Wait(); err != nil {
		return fmt.Errorf("imap: CREATE %q: %w", name, err)
	}
	return nil
}

// DeleteFolder satisfies folder.Deleter. IMAP DELETE refuses non-empty
// mailboxes on most servers, so --force first purges then deletes. The
// caller's --force is required to make a destructive intent explicit;
// without it, the server's "mailbox not empty" error bubbles unchanged.
//
// If the post-purge DELETE fails (e.g. the server refuses to delete
// INBOX, or ACL prevents removal), the messages are already gone but
// the folder isn't — the error message names that state explicitly so
// the operator isn't left wondering whether the purge ran.
func (c *Client) DeleteFolder(ctx context.Context, name string, force bool) error {
	if force {
		if err := c.PurgeFolder(ctx, name); err != nil {
			return err
		}
	}
	if err := c.c.Delete(name).Wait(); err != nil {
		if force {
			return fmt.Errorf("imap: DELETE %q failed after --force purge removed all messages; folder is now empty but still exists: %w", name, err)
		}
		return fmt.Errorf("imap: DELETE %q: %w", name, err)
	}
	return nil
}

// ExpungeFolder satisfies folder.Expunger. SELECT the folder, then
// EXPUNGE — permanently removes any message already flagged \Deleted.
// No-op if no messages carry the flag.
func (c *Client) ExpungeFolder(_ context.Context, name string) error {
	if _, err := c.c.Select(name, nil).Wait(); err != nil {
		return fmt.Errorf("imap: SELECT %q: %w", name, err)
	}
	if _, err := c.c.Expunge().Collect(); err != nil {
		return fmt.Errorf("imap: EXPUNGE %q: %w", name, err)
	}
	return nil
}

// PurgeFolder satisfies folder.Purger. The "1:*" sequence set targets
// all messages addressable in the current SELECT; an empty mailbox
// short-circuits the STORE entirely to avoid wire errors on servers
// that reject empty sequence sets.
//
// If STORE succeeds but EXPUNGE fails, the messages are flagged
// \Deleted but not yet removed; the wrapped error names that state so
// the operator knows re-running purge (or expunge) finishes the job.
func (c *Client) PurgeFolder(_ context.Context, name string) error {
	sel, err := c.c.Select(name, nil).Wait()
	if err != nil {
		return fmt.Errorf("imap: SELECT %q: %w", name, err)
	}
	flagged := false
	if sel.NumMessages > 0 {
		var set imap.SeqSet
		set.AddRange(1, 0) // 1:* — "from 1 to the highest message number"
		if _, err := c.c.Store(set, &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Flags:  []imap.Flag{imap.FlagDeleted},
			Silent: true,
		}, nil).Collect(); err != nil {
			return fmt.Errorf("imap: STORE +FLAGS.SILENT \\Deleted in %q: %w", name, err)
		}
		flagged = true
	}
	if _, err := c.c.Expunge().Collect(); err != nil {
		if flagged {
			return fmt.Errorf("imap: EXPUNGE %q failed after messages were flagged \\Deleted; re-run `mbx folder purge` (or `mbx folder expunge`) to finish removal: %w", name, err)
		}
		return fmt.Errorf("imap: EXPUNGE %q: %w", name, err)
	}
	return nil
}
