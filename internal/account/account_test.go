package account

import (
	"errors"
	"reflect"
	"testing"

	"github.com/oflabs44/mbx/internal/config"
)

func TestList_SortedByName(t *testing.T) {
	c := &config.Config{
		Accounts: map[string]*config.Account{
			"work":           {Email: "you@work.com", Backend: config.Backend{Type: config.BackendIMAP}},
			"gmail-personal": {Email: "you@gmail.com", Backend: config.Backend{Type: config.BackendGmail}, Cache: &config.Cache{Path: "/tmp/x"}},
			"side":           {Email: "you@side.com", Backend: config.Backend{Type: config.BackendIMAP}},
		},
	}

	got := List(c)
	want := []Info{
		{Name: "gmail-personal", Type: "gmail", Email: "you@gmail.com", Cache: true},
		{Name: "side", Type: "imap", Email: "you@side.com", Cache: false},
		{Name: "work", Type: "imap", Email: "you@work.com", Cache: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List mismatch.\n got: %#v\nwant: %#v", got, want)
	}
}

func TestLookup_Unknown(t *testing.T) {
	c := &config.Config{Accounts: map[string]*config.Account{"work": {Email: "x", Backend: config.Backend{Type: config.BackendIMAP}}}}
	_, _, err := Lookup(c, "nope")
	if !errors.Is(err, config.ErrUnknownAccount) {
		t.Fatalf("want ErrUnknownAccount, got %v", err)
	}
}

func TestLookup_FoundByCanonical(t *testing.T) {
	want := &config.Account{Email: "x", Backend: config.Backend{Type: config.BackendIMAP}}
	c := &config.Config{Accounts: map[string]*config.Account{"work": want}}
	cname, got, err := Lookup(c, "work")
	if err != nil {
		t.Fatalf("Lookup error: %v", err)
	}
	if got != want {
		t.Fatalf("Lookup returned different pointer")
	}
	if cname != "work" {
		t.Fatalf("canonical name = %q, want %q", cname, "work")
	}
}

func TestLookup_FoundByAlias(t *testing.T) {
	// Simulate a post-rename state: canonical name "personal-gmail",
	// alias "personal". Lookup("personal") must return the canonical
	// name "personal-gmail" so callers stamp stable IDs.
	want := &config.Account{
		Email:   "x",
		Aliases: []string{"personal"},
		Backend: config.Backend{Type: config.BackendGmail},
	}
	c := &config.Config{Accounts: map[string]*config.Account{"personal-gmail": want}}
	// Manually trigger the index build that Load would perform.
	if err := c.BuildAliasIndex(); err != nil {
		t.Fatalf("buildAliasIndex: %v", err)
	}
	cname, got, err := Lookup(c, "personal")
	if err != nil {
		t.Fatalf("Lookup via alias error: %v", err)
	}
	if got != want {
		t.Fatalf("Lookup via alias returned different pointer")
	}
	if cname != "personal-gmail" {
		t.Fatalf("canonical name = %q, want %q", cname, "personal-gmail")
	}
}
