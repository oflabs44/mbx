package message

import (
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"empty", "", ""},
		{"strips tags", "<p>hello <b>world</b></p>", "hello world"},
		{"br to newline", "line1<br>line2", "line1\nline2"},
		{"drops script", "<p>visible</p><script>alert('x')</script>", "visible"},
		{"drops style", "<style>body{color:red}</style>visible", "visible"},
		{"paragraphs separated", "<p>one</p><p>two</p><p>three</p>", "one\ntwo\nthree"},
		{"collapse whitespace", "  a   b\t\tc  ", "a b c"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HTMLToText(tc.in)
			if got != tc.want {
				t.Fatalf("HTMLToText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHTMLToTextDropsHead(t *testing.T) {
	in := `<html><head><title>x</title><style>p{}</style></head><body>visible</body></html>`
	got := HTMLToText(in)
	if !strings.Contains(got, "visible") {
		t.Fatalf("expected 'visible' in %q", got)
	}
	if strings.Contains(got, "x") || strings.Contains(got, "p{}") {
		t.Fatalf("head/style content leaked: %q", got)
	}
}
