package imap

import (
	"bytes"
	"io"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	gomsg "github.com/emersion/go-message"

	"github.com/oflabs44/mbx/internal/attachment"
	"github.com/oflabs44/mbx/internal/message"
)

// parseMessage decodes RFC 5322 bytes into the normalized message shape,
// applying the same body-source policy the Gmail backend uses (plain
// first; html-as-text fallback unless --html or --raw).
//
// go-message handles the heavy lifting: charset / transfer-encoding
// decoding to UTF-8, multipart walking, header parsing.
func parseMessage(raw []byte, opt message.ReadOptions) (message.Message, error) {
	ent, err := gomsg.Read(bytes.NewReader(raw))
	if err != nil && !gomsg.IsUnknownCharset(err) && !gomsg.IsUnknownEncoding(err) {
		return message.Message{}, err
	}

	candidates, atts, flat := walkEntity(ent)
	body, source := message.ChooseBody(candidates, opt)

	out := message.Message{
		From:        firstHeaderAddress(ent.Header, "From"),
		To:          headerAddresses(ent.Header, "To"),
		Cc:          headerAddresses(ent.Header, "Cc"),
		Subject:     headerText(ent.Header, "Subject"),
		Date:        headerDate(ent.Header),
		Body:        body,
		BodySource:  source,
		Attachments: atts,
	}
	if opt.IncludeRaw {
		out.Parts = flat
	}
	if !opt.OmitHeaders {
		out.Headers = pickHeaders(ent.Header, opt.IncludeHeaders)
	}
	return out, nil
}

// walkEntity does a single depth-first traversal producing the three
// projections the body chooser + attachment list + --raw view need.
// Mirrors the Gmail walker shape so behavior is consistent across
// backends.
func walkEntity(root *gomsg.Entity) ([]message.BodyCandidate, []attachment.Meta, []message.Part) {
	var bodies []message.BodyCandidate
	var atts []attachment.Meta
	var flat []message.Part

	_ = root.Walk(func(_ []int, ent *gomsg.Entity, walkErr error) error {
		if walkErr != nil && !gomsg.IsUnknownCharset(walkErr) && !gomsg.IsUnknownEncoding(walkErr) {
			return walkErr
		}
		mt, _, _ := ent.Header.ContentType()
		mt = strings.ToLower(mt)
		if strings.HasPrefix(mt, "multipart/") {
			return nil
		}

		filename := contentDispositionFilename(ent.Header)
		isAttachment := filename != "" || isAttachmentDisposition(ent.Header)

		if !isAttachment && (mt == "text/plain" || mt == "text/html") {
			text := readAll(ent.Body)
			if text != "" {
				bodies = append(bodies, message.BodyCandidate{MIMEType: mt, Text: text})
			}
			flat = append(flat, message.Part{
				MIME:    mt,
				Body:    text,
				Headers: flattenHeaders(ent.Header),
			})
			return nil
		}

		if isAttachment {
			data := readAll(ent.Body)
			atts = append(atts, attachment.Meta{
				Filename: filename,
				MIME:     mt,
				Size:     int64(len(data)),
			})
			flat = append(flat, message.Part{
				MIME:     mt,
				Filename: filename,
				Headers:  flattenHeaders(ent.Header),
			})
		}
		return nil
	})

	return bodies, atts, flat
}

func contentDispositionFilename(h gomsg.Header) string {
	_, params, err := h.ContentDisposition()
	if err != nil {
		return ""
	}
	return params["filename"]
}

func isAttachmentDisposition(h gomsg.Header) bool {
	disp, _, err := h.ContentDisposition()
	if err != nil {
		return false
	}
	return strings.EqualFold(disp, "attachment")
}

// readAll is best-effort: a partial body is more useful than failing
// the read entirely, and go-message has already done charset / transfer
// decoding by this point. The error is intentionally discarded.
func readAll(r io.Reader) string {
	if r == nil {
		return ""
	}
	b, _ := io.ReadAll(r)
	return string(b)
}

// firstHeaderAddress returns a "Name <addr>" form for the first address
// in the named header field, matching the envelope listing format.
func firstHeaderAddress(h gomsg.Header, key string) string {
	addrs := headerAddresses(h, key)
	if len(addrs) == 0 {
		return ""
	}
	return addrs[0]
}

func headerAddresses(h gomsg.Header, key string) []string {
	val := h.Get(key)
	if val == "" {
		return nil
	}
	parsed, err := mail.ParseAddressList(val)
	if err != nil || len(parsed) == 0 {
		return []string{val}
	}
	out := make([]string, 0, len(parsed))
	for _, a := range parsed {
		if a.Name == "" {
			out = append(out, a.Address)
		} else {
			out = append(out, a.Name+" <"+a.Address+">")
		}
	}
	return out
}

func headerText(h gomsg.Header, key string) string {
	t, err := h.Text(key)
	if err != nil {
		return h.Get(key)
	}
	return t
}

func headerDate(h gomsg.Header) time.Time {
	v := h.Get("Date")
	if v == "" {
		return time.Time{}
	}
	t, err := mail.ParseDate(v)
	if err != nil {
		return time.Time{}
	}
	return t
}

func flattenHeaders(h gomsg.Header) map[string]string {
	out := map[string]string{}
	fields := h.Fields()
	for fields.Next() {
		out[fields.Key()] = fields.Value()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func pickHeaders(h gomsg.Header, names []string) map[string]string {
	if len(names) == 0 {
		return defaultHeadersFromEntity(h)
	}
	out := map[string]string{}
	for _, n := range names {
		if v := h.Get(n); v != "" {
			out[textproto.CanonicalMIMEHeaderKey(n)] = v
		}
	}
	return out
}

func defaultHeadersFromEntity(h gomsg.Header) map[string]string {
	out := map[string]string{}
	for _, n := range []string{"Message-ID", "In-Reply-To", "References"} {
		if v := h.Get(n); v != "" {
			out[n] = v
		}
	}
	return out
}
