package gmail

import (
	"context"
	"strings"

	gmailv1 "google.golang.org/api/gmail/v1"

	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/mbxid"
)

// threadMetadataHeaders extends the envelope-level metadata set with the
// three headers BuildGraph needs to compute parent/depth. They are dropped
// after threading (not added to the JSON output contract) so the cost is
// only on the wire.
var threadMetadataHeaders = append(append([]string{}, envelopeMetadataHeaders...),
	"Message-ID", "In-Reply-To", "References")

// ThreadEnvelopes satisfies envelope.ThreadSearcher. Gmail surfaces the
// thread server-side: messages.get(format=minimal) resolves the envelope's
// threadId, then threads.get(format=metadata) returns every message. We
// run the same client-side algorithm BuildGraph uses for IMAP fallback
// to compute depths from In-Reply-To / References, so depth_map is
// consistent across providers.
//
// ThreadID is overridden with Gmail's server-supplied threadId (instead
// of BuildGraph's root-envelope id) — agents can pass it back to the API
// directly if they round-trip.
func (c *Client) ThreadEnvelopes(ctx context.Context, q envelope.ThreadQuery) (envelope.Thread, error) {
	msg, err := c.svc.Users.Messages.Get("me", q.ID.GmailMsgID).
		Format("minimal").
		Context(ctx).Do()
	if err != nil {
		return envelope.Thread{}, mapErr(err)
	}

	t, err := c.svc.Users.Threads.Get("me", msg.ThreadId).
		Format("metadata").
		MetadataHeaders(threadMetadataHeaders...).
		Context(ctx).Do()
	if err != nil {
		return envelope.Thread{}, mapErr(err)
	}

	nodes := make([]envelope.ThreadNode, 0, len(t.Messages))
	for _, m := range t.Messages {
		nodes = append(nodes, c.toThreadNode(m))
	}

	th := envelope.BuildGraph(nodes, q.ID.String())
	th.ThreadID = mbxid.NewGmail(c.Account, t.Id).String()
	return th, nil
}

func (c *Client) toThreadNode(m *gmailv1.Message) envelope.ThreadNode {
	hdr := headerLookup(m.Payload)
	return envelope.ThreadNode{
		Envelope:   c.toEnvelope(m),
		MessageID:  hdr("Message-ID"),
		InReplyTo:  hdr("In-Reply-To"),
		References: strings.Fields(hdr("References")),
	}
}
