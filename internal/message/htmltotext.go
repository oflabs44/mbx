package message

import (
	"strings"

	"golang.org/x/net/html"
)

// blockTags emit a newline boundary when the parser exits them, so
// stripped output keeps paragraph structure instead of running together.
var blockTags = map[string]bool{
	"p": true, "br": true, "div": true, "li": true, "tr": true, "td": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"hr": true, "blockquote": true, "pre": true, "section": true,
	"article": true, "header": true, "footer": true, "nav": true,
}

// dropTags and their entire subtree are skipped — script/style payloads
// would otherwise leak into the visible text.
var dropTags = map[string]bool{
	"script": true, "style": true, "head": true, "noscript": true,
}

// HTMLToText strips HTML to a plain-text rendering suitable for the
// agent surface. It's deliberately small: tokenize with x/net/html, emit
// text nodes, drop script/style/head, add newlines after block elements,
// collapse runs of whitespace. Marketing-email rendering won't be
// pixel-perfect; that's fine.
func HTMLToText(in string) string {
	if in == "" {
		return ""
	}
	tk := html.NewTokenizer(strings.NewReader(in))
	var b strings.Builder
	skipDepth := 0

	for {
		tt := tk.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken:
			tag, _ := tk.TagName()
			name := string(tag)
			if dropTags[name] {
				skipDepth++
			}
			if name == "br" {
				b.WriteByte('\n')
			}
		case html.EndTagToken:
			tag, _ := tk.TagName()
			name := string(tag)
			if dropTags[name] && skipDepth > 0 {
				skipDepth--
			}
			if blockTags[name] {
				b.WriteByte('\n')
			}
		case html.SelfClosingTagToken:
			tag, _ := tk.TagName()
			if string(tag) == "br" {
				b.WriteByte('\n')
			}
		case html.TextToken:
			if skipDepth > 0 {
				continue
			}
			b.Write(tk.Text())
		}
	}

	return collapseWhitespace(b.String())
}

// collapseWhitespace folds runs of spaces and tabs to a single space and
// runs of newlines to at most two — keeps paragraph breaks but removes
// the gaps inflated by per-tag newline emits.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	prevSpace := false
	newlineRun := 0
	for _, r := range s {
		switch {
		case r == '\n':
			newlineRun++
			prevSpace = false
			if newlineRun <= 2 {
				b.WriteByte('\n')
			}
		case r == ' ' || r == '\t':
			if newlineRun > 0 {
				continue
			}
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
		default:
			b.WriteRune(r)
			prevSpace = false
			newlineRun = 0
		}
	}
	return strings.TrimSpace(b.String())
}
