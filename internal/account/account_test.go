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
	_, err := Lookup(c, "nope")
	if !errors.Is(err, config.ErrUnknownAccount) {
		t.Fatalf("want ErrUnknownAccount, got %v", err)
	}
}

func TestLookup_Found(t *testing.T) {
	want := &config.Account{Email: "x", Backend: config.Backend{Type: config.BackendIMAP}}
	c := &config.Config{Accounts: map[string]*config.Account{"work": want}}
	got, err := Lookup(c, "work")
	if err != nil {
		t.Fatalf("Lookup error: %v", err)
	}
	if got != want {
		t.Fatalf("Lookup returned different pointer")
	}
}
