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
email = "you@gmail.com"

backend.type  = "gmail"
backend.login = "you@gmail.com"

backend.auth.type      = "oauth2"
backend.auth.client-id = "abc.apps.googleusercontent.com"
backend.auth.auth-url  = "https://accounts.google.com/o/oauth2/v2/auth"
backend.auth.token-url = "https://www.googleapis.com/oauth2/v3/token"
backend.auth.method    = "xoauth2"
backend.auth.scopes    = ["https://mail.google.com/"]

backend.auth.refresh-token.cmd       = "op read op://Dev/mbx-gmail/refresh"
backend.auth.refresh-token.write_cmd = "op item edit mbx-gmail refresh[password]=-"
`
	c, err := decodeStr(t, cfg)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	_, a, ok := c.Resolve("gmail-personal")
	if !ok {
		t.Fatal("account not found")
	}
	if a.Backend.Type != BackendGmail {
		t.Errorf("backend.type = %q, want gmail", a.Backend.Type)
	}
	if a.Message != nil && a.Message.Send != nil {
		t.Error("gmail account should not have message.send.backend")
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
downloads-dir = "~/Downloads"

[accounts.work]
email = "me@company.com"

backend.type            = "imap"
backend.host            = "imap.company.com"
backend.port            = 993
backend.encryption.type = "tls"
backend.login           = "me@company.com"
backend.auth.type       = "password"
backend.auth.cmd        = "op read op://Work/mbx-work/imap-pass"

message.send.backend.type            = "smtp"
message.send.backend.host            = "smtp.company.com"
message.send.backend.port            = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login           = "me@company.com"
message.send.backend.auth.type       = "password"
message.send.backend.auth.cmd        = "op read op://Work/mbx-work/smtp-pass"

folder.aliases.inbox  = "INBOX"
folder.aliases.sent   = "Sent"
folder.aliases.drafts = "Drafts"
folder.aliases.trash  = "Trash"

cache.path      = "~/.cache/mbx/work.db"
cache.sync_days = 90
cache.folders   = ["INBOX", "Sent"]
`
	c, err := decodeStr(t, cfg)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.HasPrefix(c.DownloadsDir, "~") {
		t.Errorf("downloads-dir not expanded: %q", c.DownloadsDir)
	}

	a := c.Accounts["work"]
	if a.Backend.Type != BackendIMAP {
		t.Errorf("backend.type = %q, want imap", a.Backend.Type)
	}
	if a.Backend.Host != "imap.company.com" || a.Backend.Port != 993 {
		t.Errorf("backend host/port = %s/%d", a.Backend.Host, a.Backend.Port)
	}
	if a.Backend.Encryption == nil || a.Backend.Encryption.Type != "tls" {
		t.Errorf("backend.encryption = %+v", a.Backend.Encryption)
	}
	if a.Message == nil || a.Message.Send == nil {
		t.Fatal("imap account should have message.send.backend")
	}
	if a.Message.Send.Backend.Type != BackendSMTP {
		t.Errorf("send.backend.type = %q, want smtp", a.Message.Send.Backend.Type)
	}
	if a.Folder == nil || a.Folder.Aliases["inbox"] != "INBOX" {
		t.Errorf("folder.aliases.inbox missing or wrong: %+v", a.Folder)
	}
	if a.Cache == nil {
		t.Fatal("cache should be set")
	}
	if !strings.HasSuffix(a.Cache.Path, "/.cache/mbx/work.db") || strings.HasPrefix(a.Cache.Path, "~") {
		t.Errorf("cache.path = %q, expected ~/ expansion to absolute", a.Cache.Path)
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
			name: "missing backend.type",
			cfg: `
[accounts.x]
email = "x@y.z"

backend.host = "h"
backend.port = 1
backend.encryption.type = "tls"
backend.login = "x@y.z"
backend.auth.type = "password"
backend.auth.cmd  = "echo p"
`,
			want: ErrMissingField,
		},
		{
			name: "unknown top-level field rejected",
			cfg: `
weird = "x"

[accounts.x]
email = "x@y.z"
backend.type = "imap"
`,
			want: ErrInvalidTOML,
		},
		{
			name: "gmail with send block rejected",
			cfg: `
[accounts.x]
email = "x@gmail.com"

backend.type  = "gmail"
backend.login = "x@gmail.com"

backend.auth.type      = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url  = "u"
backend.auth.token-url = "t"
backend.auth.refresh-token.cmd       = "echo tok"
backend.auth.refresh-token.write_cmd = "echo write"

message.send.backend.type  = "smtp"
message.send.backend.host  = "smtp.gmail.com"
message.send.backend.port  = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login = "x@gmail.com"
message.send.backend.auth.type = "password"
message.send.backend.auth.cmd  = "echo p"
`,
			want: ErrUnexpectedSection,
		},
		{
			name: "gmail with host rejected",
			cfg: `
[accounts.x]
email = "x@gmail.com"

backend.type = "gmail"
backend.host = "imap.gmail.com"
backend.login = "x@gmail.com"

backend.auth.type      = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url  = "u"
backend.auth.token-url = "t"
backend.auth.refresh-token.cmd       = "echo tok"
backend.auth.refresh-token.write_cmd = "echo write"
`,
			want: ErrUnexpectedSection,
		},
		{
			name: "imap without message.send.backend",
			cfg: `
[accounts.x]
email = "x@y.z"

backend.type            = "imap"
backend.host            = "h"
backend.port            = 1
backend.encryption.type = "tls"
backend.login           = "x@y.z"
backend.auth.type       = "password"
backend.auth.cmd        = "echo p"

folder.aliases.inbox = "INBOX"
`,
			want: ErrMissingField,
		},
		{
			name: "imap without folder.aliases.inbox",
			cfg: `
[accounts.x]
email = "x@y.z"

backend.type            = "imap"
backend.host            = "h"
backend.port            = 1
backend.encryption.type = "tls"
backend.login           = "x@y.z"
backend.auth.type       = "password"
backend.auth.cmd        = "echo p"

message.send.backend.type            = "smtp"
message.send.backend.host            = "smtp"
message.send.backend.port            = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login           = "x@y.z"
message.send.backend.auth.type       = "password"
message.send.backend.auth.cmd        = "echo p"
`,
			want: ErrMissingField,
		},
		{
			name: "ambiguous password secret",
			cfg: `
[accounts.work]
email = "x@y.z"

backend.type            = "imap"
backend.host            = "h"
backend.port            = 1
backend.encryption.type = "tls"
backend.login           = "x@y.z"
backend.auth.type       = "password"
backend.auth.raw        = "p"
backend.auth.cmd        = "echo p"

message.send.backend.type            = "smtp"
message.send.backend.host            = "smtp"
message.send.backend.port            = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login           = "x@y.z"
message.send.backend.auth.type       = "password"
message.send.backend.auth.cmd        = "echo p"

folder.aliases.inbox = "INBOX"
`,
			want: ErrAmbiguousSecret,
		},
		{
			name: "ambiguous nested secret",
			cfg: `
[accounts.g]
email = "g@x.y"

backend.type  = "gmail"
backend.login = "g@x.y"

backend.auth.type      = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url  = "u"
backend.auth.token-url = "t"

backend.auth.refresh-token.keyring   = "mbx-g"
backend.auth.refresh-token.cmd       = "echo tok"
backend.auth.refresh-token.write_cmd = "echo w"
`,
			want: ErrAmbiguousSecret,
		},
		{
			name: "flat password fields under oauth2",
			cfg: `
[accounts.g]
email = "g@x.y"

backend.type  = "gmail"
backend.login = "g@x.y"

backend.auth.type      = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url  = "u"
backend.auth.token-url = "t"
backend.auth.raw       = "leftover-from-migration"

backend.auth.refresh-token.cmd       = "echo tok"
backend.auth.refresh-token.write_cmd = "echo w"
`,
			want: ErrUnexpectedSection,
		},
		{
			name: "oauth2 fields under password",
			cfg: `
[accounts.work]
email = "x@y.z"

backend.type            = "imap"
backend.host            = "h"
backend.port            = 1
backend.encryption.type = "tls"
backend.login           = "x@y.z"
backend.auth.type       = "password"
backend.auth.cmd        = "echo p"
backend.auth.client-id  = "stray"

message.send.backend.type            = "smtp"
message.send.backend.host            = "smtp"
message.send.backend.port            = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login           = "x@y.z"
message.send.backend.auth.type       = "password"
message.send.backend.auth.cmd        = "echo p"

folder.aliases.inbox = "INBOX"
`,
			want: ErrUnexpectedSection,
		},
		{
			name: "alias collides with another account's canonical name",
			cfg: `
[accounts.a]
email = "a@y"
aliases = ["b"]

backend.type = "gmail"
backend.login = "a@y"
backend.auth.type = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url = "https://a"
backend.auth.token-url = "https://t"
backend.auth.refresh-token.cmd = "echo x"
backend.auth.refresh-token.write_cmd = "cat >/dev/null"

[accounts.b]
email = "b@y"

backend.type = "gmail"
backend.login = "b@y"
backend.auth.type = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url = "https://a"
backend.auth.token-url = "https://t"
backend.auth.refresh-token.cmd = "echo x"
backend.auth.refresh-token.write_cmd = "cat >/dev/null"
`,
			want: ErrInvalidValue,
		},
		{
			name: "two accounts claim the same alias",
			cfg: `
[accounts.a]
email = "a@y"
aliases = ["shared"]

backend.type = "gmail"
backend.login = "a@y"
backend.auth.type = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url = "https://a"
backend.auth.token-url = "https://t"
backend.auth.refresh-token.cmd = "echo x"
backend.auth.refresh-token.write_cmd = "cat >/dev/null"

[accounts.b]
email = "b@y"
aliases = ["shared"]

backend.type = "gmail"
backend.login = "b@y"
backend.auth.type = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url = "https://a"
backend.auth.token-url = "https://t"
backend.auth.refresh-token.cmd = "echo x"
backend.auth.refresh-token.write_cmd = "cat >/dev/null"
`,
			want: ErrInvalidValue,
		},
		{
			name: "self-aliasing rejected",
			cfg: `
[accounts.a]
email = "a@y"
aliases = ["a"]

backend.type = "gmail"
backend.login = "a@y"
backend.auth.type = "oauth2"
backend.auth.client-id = "id"
backend.auth.auth-url = "https://a"
backend.auth.token-url = "https://t"
backend.auth.refresh-token.cmd = "echo x"
backend.auth.refresh-token.write_cmd = "cat >/dev/null"
`,
			want: ErrInvalidValue,
		},
		{
			name: "encryption.type invalid",
			cfg: `
[accounts.work]
email = "x@y.z"

backend.type            = "imap"
backend.host            = "h"
backend.port            = 1
backend.encryption.type = "weird"
backend.login           = "x@y.z"
backend.auth.type       = "password"
backend.auth.cmd        = "echo p"

message.send.backend.type            = "smtp"
message.send.backend.host            = "smtp"
message.send.backend.port            = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login           = "x@y.z"
message.send.backend.auth.type       = "password"
message.send.backend.auth.cmd        = "echo p"

folder.aliases.inbox = "INBOX"
`,
			want: ErrInvalidValue,
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

func TestDefaultConfigDir_ResolutionOrder(t *testing.T) {
	cases := []struct {
		name string
		mbx  string
		xdg  string
		want string
	}{
		{"mbx env wins over xdg", "/tmp/mbx-explicit", "/tmp/xdg", "/tmp/mbx-explicit"},
		{"xdg falls back when mbx unset", "", "/tmp/xdg", "/tmp/xdg/mbx"},
		{"home fallback when both unset", "", "", "/.config/mbx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MBX_CONFIG_DIR", c.mbx)
			t.Setenv("XDG_CONFIG_HOME", c.xdg)

			got, err := DefaultConfigDir()
			if err != nil {
				t.Fatalf("DefaultConfigDir: %v", err)
			}
			if !strings.HasSuffix(got, c.want) {
				t.Errorf("got %q, want suffix %q", got, c.want)
			}
		})
	}
}

func TestDefaultConfigDir_ExpandsTilde(t *testing.T) {
	t.Setenv("MBX_CONFIG_DIR", "~/custom-mbx")
	t.Setenv("XDG_CONFIG_HOME", "")

	got, err := DefaultConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(got, "~") {
		t.Errorf("tilde not expanded: %q", got)
	}
	if !strings.HasSuffix(got, "/custom-mbx") {
		t.Errorf("got %q, want suffix /custom-mbx", got)
	}
}

func TestDefaultPath_ComposesConfigToml(t *testing.T) {
	t.Setenv("MBX_CONFIG_DIR", "/tmp/mbxdir")
	t.Setenv("XDG_CONFIG_HOME", "")

	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/mbxdir/config.toml" {
		t.Errorf("got %q", got)
	}
}
