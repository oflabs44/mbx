package message

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	gomail "github.com/emersion/go-message/mail"
)

// Outgoing is the wire-level view of a message ready to send: an RFC 5322
// blob plus the SMTP envelope (From / Recipients) so transports that need
// MAIL FROM and RCPT TO have them without re-parsing the body.
//
// Raw never contains a Bcc header — privacy demands Bcc addresses be
// invisible to delivered recipients. Backends that determine delivery
// from the wire body (Gmail's users.messages.send) must consult the Bcc
// slice and add a transport-local Bcc header before sending; the API
// then strips it before delivery. SMTP-style backends use Recipients
// directly and ignore Bcc here.
type Outgoing struct {
	From       string
	Recipients []string
	Bcc        []string
	Raw        []byte
}

// ComposeSpec captures the typed inputs to message composition. Body is
// always text; HTML=true switches the part's MIME to text/html (no plain
// fallback synthesized — agents should send the channel they want, and
// the agent-first audience rarely needs the multipart/alternative dance).
type ComposeSpec struct {
	From    string
	To      []string
	Cc      []string
	Bcc     []string
	Subject string
	Body    string
	HTML    bool
	ReplyTo string
	Attach  []AttachmentSpec
	Headers map[string]string
	Date    time.Time
}

// AttachmentSpec is a filesystem-rooted attachment input. Filename is
// what shows up on the recipient side; defaults to filepath.Base(Path).
type AttachmentSpec struct {
	Path     string
	Filename string
	MIME     string
}

// Compose builds an RFC 5322 message from spec. The wire output excludes
// Bcc from headers; the Bcc addresses are surfaced separately on the
// returned Outgoing (and folded into Recipients) so each transport can
// route them appropriately — SMTP via RCPT TO, Gmail via a transport-
// local Bcc header that the API strips before delivery.
func Compose(spec ComposeSpec) (Outgoing, error) {
	if err := validateSpec(spec); err != nil {
		return Outgoing{}, err
	}

	from, err := parseAddress(spec.From)
	if err != nil {
		return Outgoing{}, fmt.Errorf("from: %w", err)
	}
	to, err := parseAddresses(spec.To)
	if err != nil {
		return Outgoing{}, fmt.Errorf("to: %w", err)
	}
	cc, err := parseAddresses(spec.Cc)
	if err != nil {
		return Outgoing{}, fmt.Errorf("cc: %w", err)
	}
	bcc, err := parseAddresses(spec.Bcc)
	if err != nil {
		return Outgoing{}, fmt.Errorf("bcc: %w", err)
	}

	var h gomail.Header
	date := spec.Date
	if date.IsZero() {
		date = time.Now()
	}
	h.SetDate(date)
	h.SetAddressList("From", []*gomail.Address{from})
	h.SetAddressList("To", to)
	if len(cc) > 0 {
		h.SetAddressList("Cc", cc)
	}
	if spec.ReplyTo != "" {
		rt, err := parseAddress(spec.ReplyTo)
		if err != nil {
			return Outgoing{}, fmt.Errorf("reply-to: %w", err)
		}
		h.SetAddressList("Reply-To", []*gomail.Address{rt})
	}
	h.SetSubject(spec.Subject)
	for k, v := range spec.Headers {
		h.Set(k, v)
	}

	var buf bytes.Buffer
	w, err := gomail.CreateWriter(&buf, h)
	if err != nil {
		return Outgoing{}, fmt.Errorf("compose: create writer: %w", err)
	}
	if err := writeBody(w, spec); err != nil {
		return Outgoing{}, err
	}
	for _, a := range spec.Attach {
		if err := writeAttachment(w, a); err != nil {
			return Outgoing{}, err
		}
	}
	if err := w.Close(); err != nil {
		return Outgoing{}, fmt.Errorf("compose: close: %w", err)
	}

	bccBare := addressList(bcc)

	return Outgoing{
		From:       from.Address,
		Recipients: dedupe(append(append(addressList(to), addressList(cc)...), bccBare...)),
		Bcc:        bccBare,
		Raw:        buf.Bytes(),
	}, nil
}

// addressList projects parsed addresses to their bare address string.
func addressList(in []*gomail.Address) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, a := range in {
		out[i] = a.Address
	}
	return out
}

// dedupe drops duplicate addresses while preserving order.
func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok || s == "" {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func validateSpec(spec ComposeSpec) error {
	if strings.TrimSpace(spec.From) == "" {
		return fmt.Errorf("from is required")
	}
	if len(spec.To) == 0 {
		return fmt.Errorf("at least one --to is required")
	}
	if strings.TrimSpace(spec.Subject) == "" {
		return fmt.Errorf("subject is required")
	}
	return nil
}

func writeBody(w *gomail.Writer, spec ComposeSpec) error {
	mimeType := "text/plain"
	if spec.HTML {
		mimeType = "text/html"
	}
	var ih gomail.InlineHeader
	ih.Set("Content-Type", mime.FormatMediaType(mimeType, map[string]string{"charset": "utf-8"}))
	bw, err := w.CreateSingleInline(ih)
	if err != nil {
		return fmt.Errorf("compose: create body: %w", err)
	}
	if _, err := io.WriteString(bw, spec.Body); err != nil {
		_ = bw.Close()
		return fmt.Errorf("compose: write body: %w", err)
	}
	return bw.Close()
}

func writeAttachment(w *gomail.Writer, a AttachmentSpec) error {
	f, err := os.Open(a.Path)
	if err != nil {
		return fmt.Errorf("compose: open attachment %q: %w", a.Path, err)
	}
	defer f.Close()

	filename := a.Filename
	if filename == "" {
		filename = filepath.Base(a.Path)
	}
	mt := a.MIME
	if mt == "" {
		mt = mime.TypeByExtension(filepath.Ext(filename))
	}
	if mt == "" {
		mt = "application/octet-stream"
	}
	var ah gomail.AttachmentHeader
	ah.Set("Content-Type", mt)
	ah.SetFilename(filename)
	aw, err := w.CreateAttachment(ah)
	if err != nil {
		return fmt.Errorf("compose: create attachment writer for %q: %w", filename, err)
	}
	if _, err := io.Copy(aw, f); err != nil {
		_ = aw.Close()
		return fmt.Errorf("compose: copy attachment %q: %w", filename, err)
	}
	return aw.Close()
}

func parseAddress(s string) (*gomail.Address, error) {
	a, err := mail.ParseAddress(s)
	if err != nil {
		return nil, err
	}
	return &gomail.Address{Name: a.Name, Address: a.Address}, nil
}

func parseAddresses(in []string) ([]*gomail.Address, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*gomail.Address, 0, len(in))
	for _, s := range in {
		a, err := parseAddress(s)
		if err != nil {
			return nil, fmt.Errorf("address %q: %w", s, err)
		}
		out = append(out, a)
	}
	return out, nil
}
