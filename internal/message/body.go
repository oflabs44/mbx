package message

// Body source values surfaced as Message.BodySource. Stable strings —
// part of the JSON output contract.
const (
	BodySourcePlain      = "text/plain"
	BodySourceHTML       = "text/html"
	BodySourceHTMLAsText = "text/html-as-text"
	BodySourceRaw        = "raw"
)

// BodyCandidate is what a backend hands the chooser: a (mime, decoded
// text) pair for each text/* leaf part. Order matters — the chooser
// picks the first candidate that matches its preference, so backends
// should preserve document order.
type BodyCandidate struct {
	MIMEType string
	Text     string
}

// ChooseBody picks the body to surface and reports which source was used.
// Policy:
//
//   - --raw (IncludeRaw): no body, source = "raw". The Parts field on
//     Message carries the structure instead.
//   - --html (PreferHTML): first text/html candidate wins; falls back
//     to text/plain if there is no HTML part.
//   - default: first text/plain candidate wins; falls back to first
//     text/html candidate stripped to text via HTMLToText.
func ChooseBody(candidates []BodyCandidate, opt ReadOptions) (text, source string) {
	if opt.IncludeRaw {
		return "", BodySourceRaw
	}

	plain := findFirst(candidates, "text/plain")
	html := findFirst(candidates, "text/html")

	if opt.PreferHTML {
		if html != nil {
			return html.Text, BodySourceHTML
		}
		if plain != nil {
			return plain.Text, BodySourcePlain
		}
		return "", ""
	}

	if plain != nil {
		return plain.Text, BodySourcePlain
	}
	if html != nil {
		return HTMLToText(html.Text), BodySourceHTMLAsText
	}
	return "", ""
}

func findFirst(candidates []BodyCandidate, mime string) *BodyCandidate {
	for i := range candidates {
		if candidates[i].MIMEType == mime {
			return &candidates[i]
		}
	}
	return nil
}
