package attachment

import (
	"strings"
	"testing"

	"github.com/oflabs44/mbx/internal/mbxid"
)

func TestSplitID_Gmail(t *testing.T) {
	id, idx, err := SplitID("gmail:work:abc123:att-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.Provider != mbxid.Gmail || id.Account != "work" || id.GmailMsgID != "abc123" {
		t.Fatalf("parsed message id wrong: %+v", id)
	}
	if idx != 2 {
		t.Fatalf("index = %d, want 2", idx)
	}
}

func TestSplitID_RoundTripFormat(t *testing.T) {
	parent := mbxid.NewGmail("personal", "abc")
	got := FormatID(parent, 7)
	want := "gmail:personal:abc:att-7"
	if got != want {
		t.Fatalf("FormatID = %q, want %q", got, want)
	}
	id, idx, err := SplitID(got)
	if err != nil {
		t.Fatalf("SplitID round-trip: %v", err)
	}
	if id.String() != parent.String() || idx != 7 {
		t.Fatalf("round-trip mismatch: id=%s idx=%d", id.String(), idx)
	}
}

func TestSplitID_Errors(t *testing.T) {
	cases := map[string]string{
		"missing suffix":     "gmail:work:abc",
		"non-numeric suffix": "gmail:work:abc:att-x",
		"negative":           "gmail:work:abc:att--1",
		"bad message id":     "::att-0",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := SplitID(in)
			if err == nil {
				t.Fatalf("expected error for %q", in)
			}
			if strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("error message is empty for %q", in)
			}
		})
	}
}
