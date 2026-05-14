package imap

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/output"
)

// defaultThreadWindow caps the corpus the client-side fallback algorithm
// runs over when the server doesn't advertise THREAD=REFERENCES. The plan
// documents 1000 as a balance between completeness and round-trip cost
// on slow IMAP servers (commands.md, CONTEXT.md example dialogue).
const defaultThreadWindow = 1000

// ThreadEnvelopes satisfies envelope.ThreadSearcher. Two paths:
//
//   - Server fast path: when CAPABILITY advertises THREAD=REFERENCES we
//     issue UID THREAD over the whole mailbox, locate the tree containing
//     the anchor UID, fetch envelopes for just those UIDs, and run
//     BuildGraph for depth. The server has identified the cluster; we
//     own the depth assignment so it stays identical to the fallback.
//
//   - Client fallback: fetch the most-recent N envelopes
//     (backend.thread_window, default 1000) in the anchor's folder, then
//     run BuildGraph. The anchor is appended to the window if it sits
//     outside the recent N so the algorithm always has it to anchor on.
func (c *Client) ThreadEnvelopes(ctx context.Context, q envelope.ThreadQuery) (envelope.Thread, error) {
	if err := c.assertOwns(q.ID); err != nil {
		return envelope.Thread{}, fmt.Errorf("imap thread: %w", err)
	}
	if err := c.selectAndVerify(q.ID); err != nil {
		return envelope.Thread{}, err
	}
	anchorIDStr := q.ID.String()
	folder := q.ID.Folder
	uidValidity := q.ID.UIDValidity
	anchorUID := imap.UID(q.ID.UID)

	uids, err := c.threadUIDs(anchorUID)
	if err != nil {
		return envelope.Thread{}, err
	}
	if len(uids) == 0 {
		// selectAndVerify passed (folder+UIDVALIDITY were valid at SELECT
		// time) yet no thread references the anchor UID. The realistic
		// path is an EXPUNGE between SELECT and THREAD — the message is
		// gone, the ID won't address anything else, treat as invalidated.
		return envelope.Thread{}, output.Errorf(output.CodeProviderIDInvalidated,
			"thread anchor %s not present in mailbox (likely expunged); re-list to get a fresh id",
			anchorIDStr).WithDetails("id", anchorIDStr)
	}

	nodes, err := c.fetchThreadNodes(folder, uidValidity, uids)
	if err != nil {
		return envelope.Thread{}, err
	}
	return envelope.BuildGraph(nodes, anchorIDStr), nil
}

// threadUIDs returns the UIDs in the anchor's thread, via server THREAD
// when supported, otherwise the recent-N window in the anchor's folder.
func (c *Client) threadUIDs(anchor imap.UID) ([]imap.UID, error) {
	if c.supportsThreadReferences() {
		threads, err := c.c.UIDThread(&imapclient.ThreadOptions{
			Algorithm:      imap.ThreadReferences,
			SearchCriteria: &imap.SearchCriteria{},
		}).Wait()
		if err != nil {
			return nil, fmt.Errorf("imap: UID THREAD: %w", err)
		}
		return findThreadContaining(threads, anchor), nil
	}
	return c.threadWindowUIDs(anchor)
}

func (c *Client) supportsThreadReferences() bool {
	return slices.Contains(c.c.Caps().ThreadAlgorithms(), imap.ThreadReferences)
}

func (c *Client) threadWindowUIDs(anchor imap.UID) ([]imap.UID, error) {
	window := c.Cfg.Backend.ThreadWindow
	if window <= 0 {
		window = defaultThreadWindow
	}
	searchData, err := c.c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("imap: UID SEARCH (thread window): %w", err)
	}
	uids := searchData.AllUIDs()
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	if len(uids) > window {
		uids = uids[:window]
	}
	if anchor != 0 && !slices.Contains(uids, anchor) {
		uids = append(uids, anchor)
	}
	return uids, nil
}

// findThreadContaining walks the IMAP THREAD response forest and returns
// the flat UID list of the (sub-)tree containing anchorUID. Returns nil
// if no tree references the anchor — that should only happen if the
// server's UID THREAD set didn't include it (e.g. the message was
// EXPUNGEd between the SELECT and the THREAD call).
func findThreadContaining(threads []imapclient.ThreadData, anchor imap.UID) []imap.UID {
	for _, t := range threads {
		all := flattenThread(t)
		if slices.Contains(all, anchor) {
			return all
		}
	}
	return nil
}

func flattenThread(t imapclient.ThreadData) []imap.UID {
	out := make([]imap.UID, 0, len(t.Chain))
	for _, n := range t.Chain {
		out = append(out, imap.UID(n))
	}
	for _, sub := range t.SubThreads {
		out = append(out, flattenThread(sub)...)
	}
	return out
}

func (c *Client) fetchThreadNodes(folder string, uidValidity uint32, uids []imap.UID) ([]envelope.ThreadNode, error) {
	var set imap.UIDSet
	set.AddNum(uids...)
	cmd := c.c.Fetch(set, &imap.FetchOptions{
		UID:          true,
		Envelope:     true,
		Flags:        true,
		InternalDate: true,
		BodySection: []*imap.FetchItemBodySection{{
			Specifier:    imap.PartSpecifierHeader,
			HeaderFields: []string{"References"},
			Peek:         true,
		}},
	})
	bufs, err := cmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: UID FETCH (thread): %w", err)
	}
	nodes := make([]envelope.ThreadNode, 0, len(bufs))
	for _, buf := range bufs {
		nodes = append(nodes, c.toThreadNode(folder, uidValidity, buf))
	}
	return nodes, nil
}

func (c *Client) toThreadNode(folder string, uidValidity uint32, buf *imapclient.FetchMessageBuffer) envelope.ThreadNode {
	n := envelope.ThreadNode{Envelope: c.toEnvelope(folder, uidValidity, buf)}
	if buf.Envelope != nil {
		n.MessageID = buf.Envelope.MessageID
		if len(buf.Envelope.InReplyTo) > 0 {
			n.InReplyTo = buf.Envelope.InReplyTo[0]
		}
	}
	n.References = parseReferencesHeader(buf)
	return n
}

// parseReferencesHeader extracts the Message-IDs from the line-folded
// References header returned by FETCH BODY[HEADER.FIELDS (REFERENCES)].
// The wire form is "References: <a> <b>\r\n <c>\r\n\r\n"; strings.Fields
// collapses any whitespace (including the folding CRLF + LWSP) so we get
// the IDs without further parsing.
func parseReferencesHeader(buf *imapclient.FetchMessageBuffer) []string {
	for _, sec := range buf.BodySection {
		if len(sec.Bytes) == 0 {
			continue
		}
		_, val, ok := strings.Cut(string(sec.Bytes), ":")
		if !ok {
			continue
		}
		parts := strings.Fields(val)
		if len(parts) == 0 {
			return nil
		}
		return parts
	}
	return nil
}
