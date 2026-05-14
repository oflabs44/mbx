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
			"work":           {Type: config.AccountIMAP, Email: "you@work.com"},
			"gmail-personal": {Type: config.AccountGmail, Email: "you@gmail.com", Cache: &config.Cache{Path: "/tmp/x"}},
			"side":           {Type: config.AccountIMAP, Email: "you@side.com"},
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
	c := &config.Config{Accounts: map[string]*config.Account{"work": {Type: config.AccountIMAP, Email: "x"}}}
	_, err := Lookup(c, "nope")
	if !errors.Is(err, config.ErrUnknownAccount) {
		t.Fatalf("want ErrUnknownAccount, got %v", err)
	}
}

func TestLookup_Found(t *testing.T) {
	want := &config.Account{Type: config.AccountIMAP, Email: "x"}
	c := &config.Config{Accounts: map[string]*config.Account{"work": want}}
	got, err := Lookup(c, "work")
	if err != nil {
		t.Fatalf("Lookup error: %v", err)
	}
	if got != want {
		t.Fatalf("Lookup returned different pointer")
	}
}
