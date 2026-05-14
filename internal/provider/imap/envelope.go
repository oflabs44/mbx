package imap

import (
	"context"
	"fmt"
	"mime"
	"sort"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/mbxid"
)

// defaultFolder is the IMAP-spec-mandated inbox name. Used when the
// caller passes an empty Folder.
const defaultFolder = "INBOX"

// ListEnvelopes satisfies envelope.Lister. SELECTs the target folder
// (which gives us UIDVALIDITY for ID stamping), translates the
// ListQuery to SEARCH criteria, then UID FETCH ENVELOPE FLAGS for the
// matching UIDs.
//
// Pagination is descending-by-UID with an exclusive-upper-bound cursor:
// page N's smallest UID becomes page N+1's cursor; the next SEARCH is
// scoped to UID 1:cursor-1.
func (c *Client) ListEnvelopes(ctx context.Context, q envelope.ListQuery) (envelope.Page, error) {
	folder := q.Folder
	if folder == "" {
		folder = defaultFolder
	}

	sel, err := c.c.Select(folder, nil).Wait()
	if err != nil {
		return envelope.Page{}, fmt.Errorf("imap: SELECT %q: %w", folder, err)
	}

	criteria, err := buildCriteria(q)
	if err != nil {
		return envelope.Page{}, err
	}
	if cur, ok := parseCursor(q.Cursor); ok {
		criteria.UID = append(criteria.UID, imap.UIDSet{{Start: 1, Stop: cur - 1}})
	}

	searchData, err := c.c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return envelope.Page{}, fmt.Errorf("imap: UID SEARCH: %w", err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return envelope.Page{Envelopes: []envelope.Envelope{}}, nil
	}

	// Sort descending so the page is the newest UIDs in this query.
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	if q.Limit > 0 && len(uids) > q.Limit {
		uids = uids[:q.Limit]
	}

	bufs, err := c.fetchEnvelopes(uids)
	if err != nil {
		return envelope.Page{}, err
	}

	envelopes := make([]envelope.Envelope, 0, len(bufs))
	for _, buf := range bufs {
		envelopes = append(envelopes, c.toEnvelope(folder, sel.UIDValidity, buf))
	}
	// FetchCommand returns in server order; restore the descending order
	// the caller paginated by.
	sort.Slice(envelopes, func(i, j int) bool { return envelopes[i].Date.After(envelopes[j].Date) })

	page := envelope.Page{Envelopes: envelopes}
	// NextCursor is the smallest UID this page returned; the next call
	// will SEARCH UID 1:cursor-1 (exclusive). Empty if we hit fewer than
	// Limit results — there's no more to page through.
	if q.Limit > 0 && len(uids) == q.Limit {
		smallest := uids[len(uids)-1]
		page.NextCursor = strconv.FormatUint(uint64(smallest), 10)
	}

	return page, nil
}

func (c *Client) fetchEnvelopes(uids []imap.UID) ([]*imapclient.FetchMessageBuffer, error) {
	var set imap.UIDSet
	set.AddNum(uids...)

	cmd := c.c.Fetch(set, &imap.FetchOptions{
		UID:          true,
		Envelope:     true,
		Flags:        true,
		InternalDate: true,
	})
	bufs, err := cmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("imap: UID FETCH ENVELOPE: %w", err)
	}
	return bufs, nil
}

// buildCriteria translates an envelope.ListQuery into IMAP SEARCH
// criteria. RawQuery is wired in 3.6 — for now an explicit non-empty
// value is rejected so it's never silently dropped.
func buildCriteria(q envelope.ListQuery) (*imap.SearchCriteria, error) {
	criteria := &imap.SearchCriteria{}
	if q.Unread != nil {
		if *q.Unread {
			criteria.NotFlag = append(criteria.NotFlag, imap.FlagSeen)
		} else {
			criteria.Flag = append(criteria.Flag, imap.FlagSeen)
		}
	}
	if q.Starred != nil && *q.Starred {
		criteria.Flag = append(criteria.Flag, imap.FlagFlagged)
	}
	if q.From != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: q.From})
	}
	if q.To != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: q.To})
	}
	if !q.After.IsZero() {
		criteria.Since = q.After
	}
	if !q.Before.IsZero() {
		criteria.Before = q.Before
	}
	// HasAttachment has no portable IMAP SEARCH predicate; would require
	// a per-message BODYSTRUCTURE fetch. Skipped here — typed --from /
	// --starred / etc. cover the common cases.
	//
	// RawQuery routes to the IMAP TEXT criterion: a server-side full-text
	// search across headers and body. Users who need structured server-
	// native search (e.g. Gmail-via-IMAP's X-GM-RAW) can compose it via
	// the typed flags or wait for a per-server escape hatch.
	if q.RawQuery != "" {
		criteria.Text = append(criteria.Text, q.RawQuery)
	}
	return criteria, nil
}

func parseCursor(s string) (imap.UID, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	return imap.UID(n), true
}

// toEnvelope projects a FetchMessageBuffer onto the normalized
// envelope.Envelope shape.
func (c *Client) toEnvelope(folder string, uidValidity uint32, buf *imapclient.FetchMessageBuffer) envelope.Envelope {
	id := mbxid.NewIMAP(c.Account, folder, uidValidity, uint32(buf.UID))
	env := envelope.Envelope{
		ID:       id.String(),
		Account:  c.Account,
		Flags:    flagsFromIMAP(buf.Flags),
		Folders:  []string{folder},
		Date:     buf.InternalDate,
		Provider: string(mbxid.IMAP),
	}
	if buf.Envelope != nil {
		env.From = formatAddress(buf.Envelope.From)
		env.To = formatAddrList(buf.Envelope.To)
		env.Cc = formatAddrList(buf.Envelope.Cc)
		env.Subject = decodeRFC2047(buf.Envelope.Subject)
		// Server-supplied envelope Date (the message's Date: header) wins
		// over InternalDate when present — agents usually want the sender's
		// stamp, not the server's receipt time.
		if !buf.Envelope.Date.IsZero() {
			env.Date = buf.Envelope.Date
		}
	}
	return env
}

func formatAddrList(addrs []imap.Address) []string {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for i := range addrs {
		out = append(out, formatSingleAddress(addrs[i]))
	}
	return out
}

func formatAddress(addrs []imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	return formatSingleAddress(addrs[0])
}

func formatSingleAddress(a imap.Address) string {
	addr := a.Addr()
	if a.Name == "" {
		return addr
	}
	return fmt.Sprintf("%s <%s>", decodeRFC2047(a.Name), addr)
}

// decodeRFC2047 expands =?charset?B?...?= encoded-word headers to UTF-8.
// go-imap returns Subject and Address.Name verbatim from the wire, which
// for non-ASCII correspondents means raw encoded-words leak into JSON
// without this. Mirrors what the Gmail backend does in decodeHeaderWord.
func decodeRFC2047(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}

// flagsFromIMAP projects IMAP system flags onto the mbx Flag vocabulary
// (CONTEXT.md positive form). Server-defined keywords are dropped — the
// vocabulary is intentionally cross-provider and small.
func flagsFromIMAP(in []imap.Flag) []string {
	out := []string{}
	for _, f := range in {
		switch f {
		case imap.FlagSeen:
			out = append(out, "seen")
		case imap.FlagFlagged:
			out = append(out, "flagged")
		case imap.FlagAnswered:
			out = append(out, "answered")
		case imap.FlagDraft:
			out = append(out, "draft")
		case imap.FlagDeleted:
			out = append(out, "deleted")
		}
	}
	return out
}
