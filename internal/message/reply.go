package message

import (
	"fmt"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// ReplyOpts are the reply-time inputs that aren't derivable from the
// source message. All / Quote come from CLI flags; ReplyTo / Attach /
// HTML mirror the corresponding send-time flags. Body is the new prose
// the user is replying with (already resolved from --body / --body-file
// / --body-stdin by the cmd handler).
type ReplyOpts struct {
	From    string
	Body    string
	HTML    bool
	All     bool
	Quote   bool
	ReplyTo string
	Attach  []AttachmentSpec
}

// ForwardOpts captures the forward-time inputs not derivable from the
// source. To is required; the original message body is always quoted
// below the user's new Body.
type ForwardOpts struct {
	From    string
	To      []string
	Cc      []string
	Bcc     []string
	Body    string
	HTML    bool
	ReplyTo string
	Attach  []AttachmentSpec
}

// BuildReply assembles a ComposeSpec for a reply to source. To, Cc,
// Subject, In-Reply-To, References are derived; the user supplies only
// the new prose (and the --all / --quote knobs).
//
// To = source.From; --all adds source.To + source.Cc (minus the
// replying account's own address). References chains the source's
// existing References + Message-ID; In-Reply-To = source's Message-ID.
func BuildReply(source Message, opts ReplyOpts) (ComposeSpec, error) {
	if strings.TrimSpace(opts.From) == "" {
		return ComposeSpec{}, fmt.Errorf("reply: from is required")
	}
	to, cc, err := replyRecipients(source, opts.From, opts.All)
	if err != nil {
		return ComposeSpec{}, err
	}

	body := opts.Body
	if opts.Quote {
		body = appendQuotedOriginal(body, source)
	}

	spec := ComposeSpec{
		From:    opts.From,
		To:      to,
		Cc:      cc,
		Subject: replySubject(source.Subject),
		Body:    body,
		HTML:    opts.HTML,
		ReplyTo: opts.ReplyTo,
		Attach:  opts.Attach,
		Headers: threadingHeaders(source),
	}
	return spec, nil
}

// BuildForward assembles a ComposeSpec for a forward of source. To is
// user-supplied (required); the original body is always quoted below
// the user's new prose, and Subject is "Fwd: <original>".
//
// Threading headers (In-Reply-To / References) are NOT set on forwards
// — a forward starts a new thread for the recipients, who don't share
// the original's history.
func BuildForward(source Message, opts ForwardOpts) (ComposeSpec, error) {
	if strings.TrimSpace(opts.From) == "" {
		return ComposeSpec{}, fmt.Errorf("forward: from is required")
	}
	if len(opts.To) == 0 {
		return ComposeSpec{}, fmt.Errorf("forward: --to is required")
	}
	body := appendForwardedOriginal(opts.Body, source)
	return ComposeSpec{
		From:    opts.From,
		To:      opts.To,
		Cc:      opts.Cc,
		Bcc:     opts.Bcc,
		Subject: forwardSubject(source.Subject),
		Body:    body,
		HTML:    opts.HTML,
		ReplyTo: opts.ReplyTo,
		Attach:  opts.Attach,
	}, nil
}

// replyRecipients computes To and Cc for a reply. The replying account's
// own address (from) is filtered out so users don't reply to themselves
// when --all is used on a thread they're already part of.
func replyRecipients(source Message, from string, all bool) (to, cc []string, err error) {
	selfAddr, err := bareAddress(from)
	if err != nil {
		return nil, nil, fmt.Errorf("reply: from address: %w", err)
	}
	if strings.TrimSpace(source.From) == "" {
		return nil, nil, fmt.Errorf("reply: source message has no From header")
	}
	to = []string{source.From}
	if !all {
		return to, nil, nil
	}
	// --all: original To + Cc become the reply's Cc, minus self.
	cc = dedupeExcept(append(append([]string{}, source.To...), source.Cc...), selfAddr)
	return to, cc, nil
}

// dedupeExcept drops duplicates and any address matching excludeAddr
// (case-insensitive bare-address comparison). Preserves first-seen order.
//
// Unparseable inputs are kept verbatim with the raw string as the dedup
// key — Compose's address parser is the single canonical validator and
// will surface a clear error there. Dropping silently here would let a
// malformed Cc disappear from a reply-all without the user noticing.
func dedupeExcept(in []string, excludeAddr string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		var key string
		if addr, err := bareAddress(s); err == nil {
			if strings.EqualFold(addr, excludeAddr) {
				continue
			}
			key = strings.ToLower(addr)
		} else {
			key = s
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

// threadingHeaders builds the In-Reply-To / References pair from the
// source. Empty source Message-ID yields empty headers — the reply
// still composes, just without thread chaining (no other client will
// link it). That's acceptable: better to send than to refuse.
//
// Header lookups use the canonical MIME form (Message-Id, not the
// uppercase Message-ID a casual reader might expect), because both
// provider backends canonicalize via textproto.CanonicalMIMEHeaderKey
// before populating Message.Headers.
func threadingHeaders(source Message) map[string]string {
	msgID := strings.TrimSpace(source.Headers[textproto.CanonicalMIMEHeaderKey("Message-ID")])
	if msgID == "" {
		return nil
	}
	refs := strings.TrimSpace(source.Headers["References"])
	if refs == "" {
		refs = strings.TrimSpace(source.Headers["In-Reply-To"])
	}
	combined := msgID
	if refs != "" {
		combined = refs + " " + msgID
	}
	return map[string]string{
		"In-Reply-To": msgID,
		"References":  combined,
	}
}

// replySubject prepends "Re: " unless the source already carries it
// (case-insensitive). Avoids the "Re: Re: Re:" pileup that pre-decimation
// mail UIs are notorious for.
func replySubject(orig string) string {
	return prefixSubject(orig, "Re: ", "re:")
}

// forwardSubject prepends "Fwd: " unless already present. Unlike Re:,
// stacking Fwd: is sometimes useful (multi-hop forward chains), but the
// dominant CLI convention is single-prefix.
func forwardSubject(orig string) string {
	return prefixSubject(orig, "Fwd: ", "fwd:", "fw:")
}

// prefixSubject prepends primary to orig unless orig already starts with
// any of alts (case-insensitive). Returns the trimmed primary on empty
// input.
func prefixSubject(orig, primary string, alts ...string) string {
	o := strings.TrimSpace(orig)
	if o == "" {
		return strings.TrimSpace(primary)
	}
	low := strings.ToLower(o)
	for _, a := range alts {
		if strings.HasPrefix(low, a) {
			return o
		}
	}
	return primary + o
}

// appendQuotedOriginal returns body followed by an attribution line and
// the source body, each line prefixed with "> ". HTML bodies are
// not specially handled — agents asking for --html --quote get plain
// quoted text below their HTML, which is rare and accepted as a wart.
func appendQuotedOriginal(body string, source Message) string {
	var b strings.Builder
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(attribution(source))
	b.WriteString("\n")
	b.WriteString(quotePrefix(source.Body))
	return b.String()
}

// appendForwardedOriginal builds the "---------- Forwarded message ----------"
// block convention used by Gmail/most clients. Headers are echoed plainly
// (not prefixed) for skim-friendly reading.
func appendForwardedOriginal(body string, source Message) string {
	var b strings.Builder
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n---------- Forwarded message ----------\n")
	fmt.Fprintf(&b, "From: %s\n", source.From)
	if !source.Date.IsZero() {
		fmt.Fprintf(&b, "Date: %s\n", source.Date.Format(time.RFC1123Z))
	}
	if source.Subject != "" {
		fmt.Fprintf(&b, "Subject: %s\n", source.Subject)
	}
	if len(source.To) > 0 {
		fmt.Fprintf(&b, "To: %s\n", strings.Join(source.To, ", "))
	}
	if len(source.Cc) > 0 {
		fmt.Fprintf(&b, "Cc: %s\n", strings.Join(source.Cc, ", "))
	}
	b.WriteString("\n")
	b.WriteString(source.Body)
	return b.String()
}

func attribution(source Message) string {
	from := source.From
	if from == "" {
		from = "<unknown>"
	}
	if source.Date.IsZero() {
		return fmt.Sprintf("On an earlier date, %s wrote:", from)
	}
	return fmt.Sprintf("On %s, %s wrote:", source.Date.Format(time.RFC1123Z), from)
}

func quotePrefix(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// bareAddress strips display-name wrapping, returning just the
// "local@domain" portion.
func bareAddress(s string) (string, error) {
	a, err := mail.ParseAddress(s)
	if err != nil {
		return "", err
	}
	return a.Address, nil
}
