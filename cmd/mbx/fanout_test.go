package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/output"
)

func newFanoutCfg(t *testing.T) *config.Config {
	t.Helper()
	c := &config.Config{
		Accounts: map[string]*config.Account{
			"work":     {Email: "w@x", Backend: config.Backend{Type: config.BackendIMAP}},
			"personal": {Email: "p@x", Aliases: []string{"personal-old", "perso"}, Backend: config.Backend{Type: config.BackendGmail}},
			"getu":     {Email: "g@x", Backend: config.Backend{Type: config.BackendIMAP}},
		},
	}
	if err := c.BuildAliasIndex(); err != nil {
		t.Fatalf("BuildAliasIndex: %v", err)
	}
	return c
}

func TestResolveAccountList_CanonicalNames(t *testing.T) {
	c := newFanoutCfg(t)
	names, accts, err := resolveAccountList(c, []string{"work", "getu"})
	if err != nil {
		t.Fatalf("resolveAccountList: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"work", "getu"}) {
		t.Errorf("names = %v, want [work getu] (input order)", names)
	}
	if accts["work"] == nil || accts["getu"] == nil {
		t.Errorf("accts map missing entries: %+v", accts)
	}
}

func TestResolveAccountList_ResolvesAliases(t *testing.T) {
	c := newFanoutCfg(t)
	names, _, err := resolveAccountList(c, []string{"personal-old", "work"})
	if err != nil {
		t.Fatalf("resolveAccountList: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"personal", "work"}) {
		t.Errorf("names = %v, want [personal work] (alias → canonical)", names)
	}
}

func TestResolveAccountList_DedupsCanonicalPlusAlias(t *testing.T) {
	c := newFanoutCfg(t)
	// `personal` (canonical) + `perso` (alias of personal) + `work` —
	// must produce two entries, not three.
	names, _, err := resolveAccountList(c, []string{"personal", "perso", "work"})
	if err != nil {
		t.Fatalf("resolveAccountList: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"personal", "work"}) {
		t.Errorf("names = %v, want [personal work] (deduped)", names)
	}
}

func TestFanoutAllFailedError_ShapeAndCode(t *testing.T) {
	errs := map[string]*output.Failure{
		"work":     {Code: output.CodeProviderTimeout, Message: "boom"},
		"personal": {Code: output.CodeAuthRefreshFailed, Message: "token rejected"},
	}
	f := fanoutAllFailedError([]string{"work", "personal"}, errs)
	if f.Code != output.CodeFanoutAllFailed {
		t.Errorf("code = %q, want %q", f.Code, output.CodeFanoutAllFailed)
	}
	accts, ok := f.Details["accounts"].([]string)
	if !ok || !reflect.DeepEqual(accts, []string{"work", "personal"}) {
		t.Errorf("details.accounts = %v, want [work personal]", f.Details["accounts"])
	}
	gotErrs, ok := f.Details["errors"].(map[string]*output.Failure)
	if !ok || len(gotErrs) != 2 {
		t.Errorf("details.errors shape wrong: %T %v", f.Details["errors"], f.Details["errors"])
	}
	if output.ExitCode(f.Code) != 50 {
		t.Errorf("ExitCode(fanout.all_failed) = %d, want 50", output.ExitCode(f.Code))
	}
}

func TestResolveAccountList_UnknownAborts(t *testing.T) {
	c := newFanoutCfg(t)
	_, _, err := resolveAccountList(c, []string{"work", "nope"})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var f *output.Failure
	if !errors.As(err, &f) {
		t.Fatalf("err is not *output.Failure: %T", err)
	}
	if f.Code != output.CodeConfigUnknownAccount {
		t.Errorf("code = %q, want %q", f.Code, output.CodeConfigUnknownAccount)
	}
	if f.Details["account"] != "nope" {
		t.Errorf("details.account = %v, want \"nope\"", f.Details["account"])
	}
}
