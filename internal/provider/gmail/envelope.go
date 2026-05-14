package gmail

import (
	"context"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	gmailv1 "google.golang.org/api/gmail/v1"

	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/mbxid"
)

// metadataConcurrency caps the parallelism of envelope metadata fetches.
// 10 is well under Gmail's per-user concurrent-request limit while still
// giving useful speedup vs serial fetches at limit=20.
const metadataConcurrency = 10

// envelopeMetadataHeaders are the headers we ask Gmail to return on a
// metadata-format get. Anything not in this list is dropped on the wire.
var envelopeMetadataHeaders = []string{"From", "To", "Cc", "Subject", "Date"}

// ListEnvelopes satisfies envelope.Lister: messages.list for the IDs,
// then fan-out messages.get with format=metadata for the envelope fields.
// The metadata path never reads bodies — that's the envelope-vs-message
// split (CONTEXT.md).
func (c *Client) ListEnvelopes(ctx context.Context, q envelope.ListQuery) (envelope.Page, error) {
	call := c.svc.Users.Messages.List("me").MaxResults(int64(q.Limit))
	if q.Folder != "" {
		call = call.LabelIds(q.Folder)
	}
	if q.Cursor != "" {
		call = call.PageToken(q.Cursor)
	}
	if rq := buildQuery(q); rq != "" {
		call = call.Q(rq)
	}

	resp, err := call.Context(ctx).Do()
	if err != nil {
		return envelope.Page{}, mapErr(err)
	}
	if len(resp.Messages) == 0 {
		return envelope.Page{Envelopes: []envelope.Envelope{}, NextCursor: resp.NextPageToken}, nil
	}

	metas, err := c.batchMetadata(ctx, resp.Messages)
	if err != nil {
		return envelope.Page{}, err
	}

	// Preserve the order Gmail returned — it's sorted by internalDate
	// descending, which is what users expect. errgroup is fail-fast, so
	// if we got here every Messages id is in metas.
	out := make([]envelope.Envelope, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		out = append(out, c.toEnvelope(metas[m.Id]))
	}

	return envelope.Page{Envelopes: out, NextCursor: resp.NextPageToken}, nil
}

func (c *Client) batchMetadata(ctx context.Context, refs []*gmailv1.Message) (map[string]*gmailv1.Message, error) {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(metadataConcurrency)

	var mu sync.Mutex
	out := make(map[string]*gmailv1.Message, len(refs))

	for _, ref := range refs {
		id := ref.Id
		g.Go(func() error {
			m, err := c.svc.Users.Messages.Get("me", id).
				Format("metadata").
				MetadataHeaders(envelopeMetadataHeaders...).
				Context(gctx).Do()
			if err != nil {
				return mapErr(err)
			}
			mu.Lock()
			out[id] = m
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// toEnvelope projects a Gmail metadata-format Message onto the normalized
// envelope.Envelope shape. Headers are case-insensitive; Gmail returns
// them in canonical case but we don't rely on that.
func (c *Client) toEnvelope(m *gmailv1.Message) envelope.Envelope {
	hdr := headerLookup(m.Payload)

	env := envelope.Envelope{
		ID:       mbxid.NewGmail(c.Account, m.Id).String(),
		Account:  c.Account,
		From:     hdr("From"),
		To:       splitAddrs(hdr("To")),
		Cc:       splitAddrs(hdr("Cc")),
		Subject:  hdr("Subject"),
		Date:     parseInternalDate(m.InternalDate),
		Flags:    flagsFromLabels(m.LabelIds),
		Folders:  foldersFromLabels(m.LabelIds),
		Snippet:  m.Snippet,
		Provider: string(mbxid.Gmail),
	}
	if m.ThreadId != "" {
		env.ThreadID = mbxid.NewGmail(c.Account, m.ThreadId).String()
	}
	if len(m.LabelIds) > 0 {
		env.Gmail = &envelope.GmailExtras{Labels: append([]string(nil), m.LabelIds...)}
	}

	return env
}

// headerLookup returns a case-insensitive accessor over the payload's
// header list. Gmail returns headers as a flat slice; building the map
// once per message is cheaper than scanning per call.
func headerLookup(p *gmailv1.MessagePart) func(name string) string {
	if p == nil {
		return func(string) string { return "" }
	}
	m := make(map[string]string, len(p.Headers))
	for _, h := range p.Headers {
		m[strings.ToLower(h.Name)] = h.Value
	}
	return func(name string) string {
		return m[strings.ToLower(name)]
	}
}

// splitAddrs is a deliberately simple address splitter. Gmail returns
// header values verbatim; full RFC 5322 group/quoted parsing belongs in
// internal/message where we already need it for body work. For envelope
// listings, the comma-split rendering is what users expect.
func splitAddrs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseInternalDate converts Gmail's epoch-millisecond timestamp to a UTC
// time.Time. A zero value yields the zero Time, which our omitzero JSON
// tag drops on output.
func parseInternalDate(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
