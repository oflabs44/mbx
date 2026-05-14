package mbxid

import (
	"errors"
	"testing"
)

func assertRoundTrip(t *testing.T, id ID, wantStr string) {
	t.Helper()
	s := id.String()
	if s != wantStr {
		t.Errorf("String() = %q, want %q", s, wantStr)
	}
	got, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got != id {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, id)
	}
}

func TestRoundTrip_Gmail(t *testing.T) {
	assertRoundTrip(t,
		NewGmail("gmail-personal", "18f3c2a9b1d4e7f0"),
		"gmail:gmail-personal:18f3c2a9b1d4e7f0",
	)
}

func TestRoundTrip_IMAP(t *testing.T) {
	cases := []struct {
		name        string
		account     string
		folder      string
		uidValidity uint32
		uid         uint32
		wantStr     string
	}{
		{
			name:    "simple INBOX",
			account: "work", folder: "INBOX", uidValidity: 1, uid: 42,
			wantStr: "imap:work:INBOX:1:42",
		},
		{
			name:    "folder with hierarchy slash preserved",
			account: "work", folder: "INBOX/Receipts", uidValidity: 7, uid: 1001,
			wantStr: "imap:work:INBOX/Receipts:7:1001",
		},
		{
			name:    "folder with colon escaped",
			account: "work", folder: "Weird:Name", uidValidity: 1, uid: 9,
			wantStr: "imap:work:Weird%3AName:1:9",
		},
		{
			name:    "folder with percent escaped",
			account: "work", folder: "100% off", uidValidity: 1, uid: 9,
			wantStr: "imap:work:100%25 off:1:9",
		},
		{
			name:    "folder with both colon and percent",
			account: "work", folder: "a:b%c", uidValidity: 1, uid: 9,
			wantStr: "imap:work:a%3Ab%25c:1:9",
		},
		{
			name:    "max uidvalidity and uid",
			account: "work", folder: "X", uidValidity: 4294967295, uid: 4294967295,
			wantStr: "imap:work:X:4294967295:4294967295",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertRoundTrip(t,
				NewIMAP(c.account, c.folder, c.uidValidity, c.uid),
				c.wantStr,
			)
		})
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "", ErrInvalidFormat},
		{"no colon", "gmail", ErrInvalidFormat},
		{"unknown provider", "ews:work:abc", ErrUnknownProvider},
		{"gmail missing msg id", "gmail:work:", ErrInvalidFormat},
		{"gmail missing account", "gmail::abc", ErrInvalidFormat},
		{"imap too few segments", "imap:work:INBOX:1", ErrInvalidFormat},
		{"imap too many segments", "imap:work:Plain:With:Unescaped:1:42", ErrInvalidFormat},
		{"imap uidvalidity not numeric", "imap:work:INBOX:abc:42", ErrInvalidFormat},
		{"imap uid not numeric", "imap:work:INBOX:1:xx", ErrInvalidFormat},
		{"imap uidvalidity overflow", "imap:work:INBOX:99999999999:42", ErrInvalidFormat},
		{"imap empty folder", "imap:work::1:42", ErrInvalidFormat},
		{"truncated folder escape", "imap:work:foo%3:1:42", ErrInvalidFormat},
		{"unknown folder escape", "imap:work:foo%XX:1:42", ErrInvalidFormat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.in)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestEscape_NoEscapeNeeded(t *testing.T) {
	in := "INBOX/Sent/Archive"
	if got := escape(in); got != in {
		t.Errorf("escape(%q) = %q, want unchanged", in, got)
	}
}
