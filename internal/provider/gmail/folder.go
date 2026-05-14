package gmail

import (
	"context"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/oflabs44/mbx/internal/folder"
)

// folderConcurrency caps Labels.Get fan-out. The list endpoint returns
// label shells; counts require a per-label follow-up, and accounts with
// many user labels would noticeably stall on a serial loop.
const folderConcurrency = 10

// ListFolders satisfies folder.Lister. labels.list returns shells; we
// fan-out labels.get to pick up message/thread counts. Sorted by name
// for deterministic output.
func (c *Client) ListFolders(ctx context.Context) ([]folder.Folder, error) {
	resp, err := c.svc.Users.Labels.List("me").Context(ctx).Do()
	if err != nil {
		return nil, mapErr(err)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(folderConcurrency)
	var mu sync.Mutex
	out := make([]folder.Folder, 0, len(resp.Labels))

	for _, lbl := range resp.Labels {
		if labelsHiddenFromFolders[lbl.Id] {
			continue
		}
		id := lbl.Id
		g.Go(func() error {
			full, err := c.svc.Users.Labels.Get("me", id).Context(gctx).Do()
			if err != nil {
				return mapErr(err)
			}
			f := folder.Folder{
				Name:   full.Name,
				Count:  full.MessagesTotal,
				Unread: full.MessagesUnread,
				Gmail:  &folder.GmailFolderExtras{ThreadsTotal: full.ThreadsTotal},
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
