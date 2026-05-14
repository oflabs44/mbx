package imap

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"

	"github.com/oflabs44/mbx/internal/attachment"
	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/message"
)

// ReadMessage satisfies message.Reader. SELECTs the folder embedded in
// the mbx ID, verifies UIDVALIDITY hasn't rolled, FETCHes BODY[] and
// FLAGS, then parses the MIME tree via go-message (parse.go).
func (c *Client) ReadMessage(ctx context.Context, id mbxid.ID, opt message.ReadOptions) (message.Message, error) {
	if err := c.assertOwns(id); err != nil {
		return message.Message{}, err
	}
	if err := c.selectAndVerify(id); err != nil {
		return message.Message{}, err
	}

	raw, err := c.fetchBody(id.UID)
	if err != nil {
		return message.Message{}, err
	}

	out, err := parseMessage(raw, opt)
	if err != nil {
		return message.Message{}, fmt.Errorf("imap: parse message %s: %w", id.String(), err)
	}
	out.ID = id.String()
	out.Account = c.Account
	out.Provider = string(mbxid.IMAP)
	out.Attachments = attachment.Stamp(id, out.Attachments)
	out.ThreadID = "" // IMAP threading lands in phase 5

	if opt.MarkSeen {
		// Best-effort STORE +FLAGS — failure shouldn't fail the read.
		_ = c.markSeen(id.UID)
	}

	return out, nil
}

// ReadMessageRaw satisfies message.RawReader. Returns BODY[] verbatim;
// no MIME parse.
func (c *Client) ReadMessageRaw(ctx context.Context, id mbxid.ID) ([]byte, error) {
	if err := c.assertOwns(id); err != nil {
		return nil, err
	}
	if err := c.selectAndVerify(id); err != nil {
		return nil, err
	}
	return c.fetchBody(id.UID)
}

func (c *Client) fetchBody(uid uint32) ([]byte, error) {
	set := imap.UIDSetNum(imap.UID(uid))
	// Peek so the server doesn't auto-set \Seen — markSeen handles that
	// explicitly, gated on the caller's --preview flag.
	bodySection := &imap.FetchItemBodySection{Peek: true}
	cmd := c.c.Fetch(set, &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{bodySection},
	})
	bufs, err := cmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: UID FETCH BODY[]: %w", err)
	}
	if len(bufs) == 0 {
		return nil, fmt.Errorf("imap: message UID %d not found", uid)
	}
	body := bufs[0].FindBodySection(bodySection)
	if body == nil {
		return nil, fmt.Errorf("imap: server returned no BODY[] for UID %d", uid)
	}
	return body, nil
}

func (c *Client) markSeen(uid uint32) error {
	set := imap.UIDSetNum(imap.UID(uid))
	_, err := c.c.Store(set, &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagSeen},
		Silent: true,
	}, nil).Collect()
	return err
}
