package gmail

import (
	"context"
	"encoding/base64"
	"mime"
	"net/textproto"
	"slices"
	"strings"

	"golang.org/x/text/encoding/charmap"
	gmailv1 "google.golang.org/api/gmail/v1"

	"github.com/oflabs44/mbx/internal/attachment"
	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/message"
)

// ReadMessage satisfies message.Reader. Pulls format=full so the payload
// tree (and attachment metadata) is available, walks it once to collect
// body candidates and attachment metadata, then lets the domain layer
// pick the surface body via message.ChooseBody.
func (c *Client) ReadMessage(ctx context.Context, id mbxid.ID, opt message.ReadOptions) (message.Message, error) {
	if err := c.assertOwns(id); err != nil {
		return message.Message{}, err
	}
	m, err := c.svc.Users.Messages.Get("me", id.GmailMsgID).Format("full").Context(ctx).Do()
	if err != nil {
		return message.Message{}, mapErr(err)
	}

	candidates, attachments, allParts := walkParts(m.Payload)
	body, source := message.ChooseBody(candidates, opt)

	hdr := headerLookup(m.Payload)
	out := message.Message{
		ID:          mbxid.NewGmail(c.Account, m.Id).String(),
		Account:     c.Account,
		From:        hdr("From"),
		To:          splitAddrs(hdr("To")),
		Cc:          splitAddrs(hdr("Cc")),
		Subject:     decodeHeaderWord(hdr("Subject")),
		Date:        parseInternalDate(m.InternalDate),
		Body:        body,
		BodySource:  source,
		Attachments: attachAttachmentIDs(c.Account, m.Id, attachments),
		Provider:    string(mbxid.Gmail),
	}
	if m.ThreadId != "" {
		out.ThreadID = mbxid.NewGmail(c.Account, m.ThreadId).String()
	}
	if opt.IncludeRaw {
		out.Parts = allParts
	}
	if !opt.OmitHeaders {
		out.Headers = pickHeaders(m.Payload, opt.IncludeHeaders)
	}

	if opt.MarkSeen && slices.Contains(m.LabelIds, "UNREAD") {
		// messages.modify is the only path that drops UNREAD; format=full
		// does not. Failure to mark-as-seen is best-effort: a transient
		// hiccup shouldn't poison a successful read, so we swallow the
		// error here. The user re-reads if it matters.
		_, _ = c.svc.Users.Messages.Modify("me", id.GmailMsgID, &gmailv1.ModifyMessageRequest{
			RemoveLabelIds: []string{"UNREAD"},
		}).Context(ctx).Do()
	}

	return out, nil
}

// ReadMessageRaw satisfies message.RawReader. Pulls format=raw and
// base64url-decodes the wire bytes; the result is RFC 5322.
func (c *Client) ReadMessageRaw(ctx context.Context, id mbxid.ID) ([]byte, error) {
	if err := c.assertOwns(id); err != nil {
		return nil, err
	}
	m, err := c.svc.Users.Messages.Get("me", id.GmailMsgID).Format("raw").Context(ctx).Do()
	if err != nil {
		return nil, mapErr(err)
	}
	return base64.URLEncoding.DecodeString(m.Raw)
}

// gmailAttachment is the per-attachment data we collect during the walk
// before turning into the public attachment.Meta. The slice index is
// the part's position; we don't need to carry it on the struct.
type gmailAttachment struct {
	Filename string
	MIME     string
	Size     int64
	GmailID  string
}

// walkParts traverses a Gmail MessagePart tree and returns three
// projections: body candidates (for the body chooser), attachments (for
// the attachment list), and the flat parts list (for --raw output).
func walkParts(p *gmailv1.MessagePart) ([]message.BodyCandidate, []gmailAttachment, []message.Part) {
	var bodies []message.BodyCandidate
	var atts []gmailAttachment
	var flat []message.Part

	var visit func(part *gmailv1.MessagePart)
	visit = func(part *gmailv1.MessagePart) {
		if part == nil {
			return
		}
		mt := strings.ToLower(part.MimeType)

		// Containers (multipart/*) carry no payload of their own —
		// recurse into children only.
		if strings.HasPrefix(mt, "multipart/") {
			for _, ch := range part.Parts {
				visit(ch)
			}
			return
		}

		filename := part.Filename
		isAttachment := filename != "" || isAttachmentDisposition(part.Headers)

		if !isAttachment && (mt == "text/plain" || mt == "text/html") {
			text, _ := decodePartBody(part)
			if text != "" {
				bodies = append(bodies, message.BodyCandidate{MIMEType: mt, Text: text})
			}
			flat = append(flat, message.Part{
				MIME:    mt,
				Body:    text,
				Headers: flattenHeaders(part.Headers),
			})
			return
		}

		if isAttachment {
			atts = append(atts, gmailAttachment{
				Filename: filename,
				MIME:     part.MimeType,
				Size:     part.Body.Size,
				GmailID:  part.Body.AttachmentId,
			})
			flat = append(flat, message.Part{
				MIME:     part.MimeType,
				Filename: filename,
				Headers:  flattenHeaders(part.Headers),
			})
		}
	}
	visit(p)

	return bodies, atts, flat
}

// decodePartBody handles the base64url decode and the small set of
// charset conversions phase 2 commits to (UTF-8, ISO-8859-1, Windows-1252).
// Anything else is treated as UTF-8 with replacement; callers see no
// error but garbled output is possible (rare in practice).
func decodePartBody(p *gmailv1.MessagePart) (string, error) {
	if p.Body == nil || p.Body.Data == "" {
		return "", nil
	}
	raw, err := base64.URLEncoding.DecodeString(p.Body.Data)
	if err != nil {
		return "", err
	}

	charset := strings.ToLower(charsetParam(p.Headers))
	switch charset {
	case "", "utf-8", "us-ascii", "ascii":
		return string(raw), nil
	case "iso-8859-1", "latin1", "latin-1":
		return decodeBytes(raw, charmap.ISO8859_1), nil
	case "windows-1252", "cp1252":
		return decodeBytes(raw, charmap.Windows1252), nil
	default:
		return string(raw), nil
	}
}

// decodeBytes converts bytes from a single-byte charmap to UTF-8.
// charmap decoders never error on byte input (every byte is a valid
// codepoint in single-byte encodings), so the err return is safely
// discarded.
func decodeBytes(b []byte, e *charmap.Charmap) string {
	out, _ := e.NewDecoder().Bytes(b)
	return string(out)
}

func charsetParam(headers []*gmailv1.MessagePartHeader) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Content-Type") {
			_, params, err := mime.ParseMediaType(h.Value)
			if err != nil {
				return ""
			}
			return params["charset"]
		}
	}
	return ""
}

func isAttachmentDisposition(headers []*gmailv1.MessagePartHeader) bool {
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Content-Disposition") {
			disp, _, err := mime.ParseMediaType(h.Value)
			if err == nil && strings.EqualFold(disp, "attachment") {
				return true
			}
		}
	}
	return false
}

func flattenHeaders(headers []*gmailv1.MessagePartHeader) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for _, h := range headers {
		out[h.Name] = h.Value
	}
	return out
}

func pickHeaders(p *gmailv1.MessagePart, names []string) map[string]string {
	if p == nil {
		return nil
	}
	if len(names) == 0 {
		return defaultHeaders(p)
	}
	hdr := headerLookup(p)
	out := make(map[string]string, len(names))
	for _, n := range names {
		if v := hdr(n); v != "" {
			out[textproto.CanonicalMIMEHeaderKey(n)] = v
		}
	}
	return out
}

// defaultHeaders returns the threading-relevant headers most callers
// will want when --header isn't passed and --no-headers isn't set.
func defaultHeaders(p *gmailv1.MessagePart) map[string]string {
	hdr := headerLookup(p)
	out := map[string]string{}
	for _, n := range []string{"Message-ID", "In-Reply-To", "References"} {
		if v := hdr(n); v != "" {
			out[n] = v
		}
	}
	return out
}

// decodeHeaderWord runs RFC 2047 decoded-word expansion over a header
// value. Gmail returns most headers already decoded but Subject in
// particular sometimes ships the raw =?utf-8?...?= form.
func decodeHeaderWord(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}

// attachAttachmentIDs converts the internal gmailAttachment list into
// the public attachment.Meta shape and stamps each with its mbx ID.
// gmailAttachment can't go entirely — DownloadAttachment needs the
// per-part GmailID for users.messages.attachments.get.
func attachAttachmentIDs(account, msgID string, atts []gmailAttachment) []attachment.Meta {
	metas := make([]attachment.Meta, 0, len(atts))
	for _, a := range atts {
		metas = append(metas, attachment.Meta{Filename: a.Filename, Size: a.Size, MIME: a.MIME})
	}
	return attachment.Stamp(mbxid.NewGmail(account, msgID), metas)
}
