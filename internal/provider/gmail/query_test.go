package gmail

import (
	"testing"
	"time"

	"github.com/oflabs44/mbx/internal/envelope"
)

func ptrBool(b bool) *bool { return &b }

func TestBuildQuery(t *testing.T) {
	march5, _ := time.Parse("2006-01-02", "2026-03-05")

	cases := []struct {
		name string
		q    envelope.ListQuery
		want string
	}{
		{"empty", envelope.ListQuery{}, ""},
		{"unread true", envelope.ListQuery{Unread: ptrBool(true)}, "is:unread"},
		{"unread false", envelope.ListQuery{Unread: ptrBool(false)}, "is:read"},
		{"starred set false drops filter", envelope.ListQuery{Starred: ptrBool(false)}, ""},
		{"has-attachment true", envelope.ListQuery{HasAttachment: ptrBool(true)}, "has:attachment"},
		{"from no spaces", envelope.ListQuery{From: "alice@x.com"}, "from:alice@x.com"},
		{"from with spaces gets quoted", envelope.ListQuery{From: "Alice Doe"}, `from:"Alice Doe"`},
		{"after only", envelope.ListQuery{After: march5}, "after:2026/03/05"},
		{"raw query passes through", envelope.ListQuery{RawQuery: "subject:invoice"}, "subject:invoice"},
		{
			"composed AND",
			envelope.ListQuery{
				Unread:        ptrBool(true),
				HasAttachment: ptrBool(true),
				From:          "cfo@x.com",
				After:         march5,
				RawQuery:      "subject:Q2",
			},
			"is:unread has:attachment from:cfo@x.com after:2026/03/05 subject:Q2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildQuery(tc.q)
			if got != tc.want {
				t.Fatalf("buildQuery(%+v) = %q, want %q", tc.q, got, tc.want)
			}
		})
	}
}
