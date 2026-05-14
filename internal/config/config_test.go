package config

import (
	"errors"
	"strings"
	"testing"
)

func decodeStr(t *testing.T, s string) (*Config, error) {
	t.Helper()
	return decode(strings.NewReader(s), "test.toml")
}

func TestLoad_GmailAccount(t *testing.T) {
	const cfg = `
[accounts.gmail-personal]
type = "gmail"
email = "you@gmail.com"

[accounts.gmail-personal.backend.auth]
type = "oauth2"
client-id = "abc.apps.googleusercontent.com"
auth-url = "https://accounts.google.com/o/oauth2/v2/auth"
token-url = "https://www.googleapis.com/oauth2/v3/token"
method = "xoauth2"
scopes = ["https://mail.google.com/"]

[accounts.gmail-personal.backend.auth.refresh-token]
cmd = "op read op://Dev/mbx-gmail/refresh"
write_cmd = "op item edit mbx-gmail refresh[password]=-"
`
	c, err := decodeStr(t, cfg)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	a, ok := c.Account("gmail-personal")
	if !ok {
		t.Fatal("account not found")
	}
	if a.Type != AccountGmail {
		t.Errorf("type = %q, want gmail", a.Type)
	}
	if a.Send != nil {
		t.Error("gmail account should not have send block")
	}
	v, val := a.Backend.Auth.RefreshToken.Variant()
	if v != SecretCmd {
		t.Errorf("refresh-token variant = %d, want SecretCmd", v)
	}
	if !strings.HasPrefix(val, "op read") {
		t.Errorf("refresh-token value = %q", val)
	}
	if !a.Backend.Auth.RefreshToken.HasWriteCmd() {
		t.Error("refresh-token write_cmd missing")
	}
}

func TestLoad_IMAPAccountWithSendAndCache(t *testing.T) {
	const cfg = `
[accounts.work]
type = "imap"
email = "me@company.com"

[accounts.work.backend]
host = "imap.company.com"
port = 993

[accounts.work.backend.auth]
type = "password"
cmd = "op read op://Work/mbx-work/imap-pass"

[accounts.work.send]
host = "smtp.company.com"
port = 587

[accounts.work.cache]
path = "~/.cache/mbx/work.db"
sync_days = 90
folders = ["INBOX", "Sent"]
`
	c, err := decodeStr(t, cfg)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	a := c.Accounts["work"]
	if a.Backend.Host != "imap.company.com" || a.Backend.Port != 993 {
		t.Errorf("backend host/port = %s/%d", a.Backend.Host, a.Backend.Port)
	}
	if a.Send == nil {
		t.Fatal("imap account should have send block")
	}
	if a.Send.Auth != nil {
		t.Error("send.auth should be nil (inherits from backend.auth)")
	}
	if a.Cache == nil {
		t.Fatal("cache should be set")
	}
	if !strings.HasSuffix(a.Cache.Path, "/.cache/mbx/work.db") {
		t.Errorf("cache.path = %q, expected ~/ expansion to absolute", a.Cache.Path)
	}
	if strings.HasPrefix(a.Cache.Path, "~") {
		t.Errorf("cache.path = %q, ~ should be expanded", a.Cache.Path)
	}
}

func TestLoad_Errors(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		want error
	}{
		{
			name: "no accounts",
			cfg:  ``,
			want: ErrMissingField,
		},
		{
			name: "missing type",
			cfg: `
[accounts.x]
email = "x@y.z"

[accounts.x.backend.auth]
type = "password"
cmd = "echo p"
`,
			want: ErrMissingField,
		},
		{
			name: "unknown field rejected",
			cfg: `
[accounts.x]
type = "imap"
email = "x@y.z"
something_unknown = "nope"

[accounts.x.backend]
host = "h"
port = 1

[accounts.x.backend.auth]
type = "password"
cmd = "echo p"
`,
			want: ErrInvalidTOML,
		},
		{
			name: "gmail with send block rejected",
			cfg: `
[accounts.x]
type = "gmail"
email = "x@gmail.com"

[accounts.x.backend.auth]
type = "oauth2"
client-id = "id"
auth-url = "u"
token-url = "t"

[accounts.x.backend.auth.refresh-token]
cmd = "echo tok"

[accounts.x.send]
host = "smtp.gmail.com"
port = 587
`,
			want: ErrUnexpectedSection,
		},
		{
			name: "ambiguous password secret",
			cfg: `
[accounts.work]
type = "imap"
email = "x@y.z"

[accounts.work.backend]
host = "h"
port = 1

[accounts.work.backend.auth]
type = "password"
raw = "p"
cmd = "echo p"
`,
			want: ErrAmbiguousSecret,
		},
		{
			name: "ambiguous nested secret",
			cfg: `
[accounts.g]
type = "gmail"
email = "g@x.y"

[accounts.g.backend.auth]
type = "oauth2"
client-id = "id"
auth-url = "u"
token-url = "t"

[accounts.g.backend.auth.refresh-token]
keyring = "mbx-g"
cmd = "echo tok"
`,
			want: ErrAmbiguousSecret,
		},
		{
			name: "flat password fields under oauth2",
			cfg: `
[accounts.g]
type = "gmail"
email = "g@x.y"

[accounts.g.backend.auth]
type = "oauth2"
client-id = "id"
auth-url = "u"
token-url = "t"
raw = "leftover-from-migration"

[accounts.g.backend.auth.refresh-token]
cmd = "echo tok"
`,
			want: ErrUnexpectedSection,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeStr(t, tc.cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSecret_Variant(t *testing.T) {
	cases := []struct {
		name string
		s    Secret
		want SecretVariant
	}{
		{"none", Secret{}, SecretNone},
		{"raw", Secret{Raw: "x"}, SecretRaw},
		{"keyring", Secret{Keyring: "x"}, SecretKeyring},
		{"cmd", Secret{Cmd: "x"}, SecretCmd},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := c.s.Variant()
			if got != c.want {
				t.Errorf("variant = %d, want %d", got, c.want)
			}
		})
	}
}
