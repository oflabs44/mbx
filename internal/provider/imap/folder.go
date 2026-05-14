package imap

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/emersion/go-imap/v2"
	"golang.org/x/sync/errgroup"

	"github.com/oflabs44/mbx/internal/folder"
)

// statusConcurrency caps STATUS fan-out. LIST returns mailbox shells;
// counts come from a per-mailbox STATUS, which serializes badly on
// accounts with many folders.
const statusConcurrency = 10

// ListFolders satisfies folder.Lister. LIST returns the mailbox names;
// per-mailbox counts come from STATUS. Servers that advertise
// IMAP4rev2 or LIST-STATUS could batch this, but the parallel-STATUS
// path works against every server and is fast enough.
func (c *Client) ListFolders(ctx context.Context) ([]folder.Folder, error) {
	listed, err := c.c.List("", "*", nil).Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: LIST: %w", err)
	}

	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(statusConcurrency)
	var mu sync.Mutex
	out := make([]folder.Folder, 0, len(listed))

	for _, ld := range listed {
		if hasNoSelectAttr(ld.Attrs) {
			// \Noselect mailboxes are pure namespace nodes — they don't
			// hold messages and STATUS will reject them.
			continue
		}
		mailbox := ld.Mailbox
		g.Go(func() error {
			st, err := c.c.Status(mailbox, &imap.StatusOptions{
				NumMessages: true,
				NumUnseen:   true,
			}).Wait()
			if err != nil {
				return fmt.Errorf("imap: STATUS %q: %w", mailbox, err)
			}
			f := folder.Folder{Name: mailbox}
			if st.NumMessages != nil {
				f.Count = int64(*st.NumMessages)
			}
			if st.NumUnseen != nil {
				f.Unread = int64(*st.NumUnseen)
			}
			mu.Lock()
			out = append(out, f)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func hasNoSelectAttr(attrs []imap.MailboxAttr) bool {
	return slices.Contains(attrs, imap.MailboxAttrNoSelect)
}
