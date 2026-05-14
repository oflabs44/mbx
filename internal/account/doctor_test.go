package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oflabs44/mbx/internal/config"
)

func TestDoctor_UnknownAccount(t *testing.T) {
	c := &config.Config{Accounts: map[string]*config.Account{
		"work": passwordAccount(t, "ok-secret"),
	}}
	_, err := Doctor(context.Background(), c, "nope")
	if !errors.Is(err, config.ErrUnknownAccount) {
		t.Fatalf("want ErrUnknownAccount, got %v", err)
	}
}

func TestDoctor_PasswordHappyPath(t *testing.T) {
	c := &config.Config{Accounts: map[string]*config.Account{
		"work": passwordAccount(t, "any-secret"),
	}}
	r, err := Doctor(context.Background(), c, "work")
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if r.Config != StatusOK || r.Secrets != StatusOK || r.Auth != StatusOK {
		t.Errorf("statuses = %+v, want all ok", r)
	}
	if r.Connectivity != StatusWarn {
		t.Errorf("connectivity = %q, want warn (stubbed in phase 1)", r.Connectivity)
	}
	if r.Capabilities != nil {
		t.Errorf("capabilities = %+v, want nil (stubbed in phase 1)", r.Capabilities)
	}
	if len(r.Errors) != 0 {
		t.Errorf("errors = %+v, want empty", r.Errors)
	}
}

func TestDoctor_PasswordSecretFails(t *testing.T) {
	a := passwordAccount(t, "")
	// Replace the raw secret with a cmd that doesn't exist on PATH.
	a.Backend.Auth.Raw = ""
	a.Backend.Auth.Cmd = "this-command-does-not-exist-mbx-doctor-test"

	c := &config.Config{Accounts: map[string]*config.Account{"work": a}}
	r, err := Doctor(context.Background(), c, "work")
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if r.Secrets != StatusFail {
		t.Errorf("secrets = %q, want fail", r.Secrets)
	}
	if r.Auth != StatusSkip {
		t.Errorf("auth = %q, want skip", r.Auth)
	}
	if got := r.Errors["secrets"]; !strings.Contains(got, "backend.auth") {
		t.Errorf("errors[secrets] = %q, expected to mention backend.auth", got)
	}
}

func TestDoctor_OAuthMissingWriteCmd(t *testing.T) {
	c := &config.Config{Accounts: map[string]*config.Account{
		"gmail": oauthAccountNoWriteCmd(t),
	}}
	r, err := Doctor(context.Background(), c, "gmail")
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if r.Secrets != StatusOK {
		t.Errorf("secrets = %q, want ok (resolution succeeds)", r.Secrets)
	}
	if r.Auth != StatusFail {
		t.Errorf("auth = %q, want fail (no write_cmd)", r.Auth)
	}
	if got := r.Errors["auth"]; !strings.Contains(got, "write_cmd") {
		t.Errorf("errors[auth] = %q, expected mention of write_cmd", got)
	}
}

// passwordAccount builds a minimal IMAP+password account with all required
// fields populated and a raw secret of `value` on the backend auth.
func passwordAccount(t *testing.T, value string) *config.Account {
	t.Helper()
	return &config.Account{
		Email: "you@example.com",
		Backend: config.Backend{
			Type:       config.BackendIMAP,
			Host:       "imap.example.com",
			Port:       993,
			Encryption: &config.Encryption{Type: config.EncryptionTLS},
			Login:      "you@example.com",
			Auth: config.Auth{
				Type: config.AuthPassword,
				Raw:  value,
			},
		},
		Message: &config.Message{Send: &config.MessageSend{Backend: config.Backend{
			Type:       config.BackendSMTP,
			Host:       "smtp.example.com",
			Port:       587,
			Encryption: &config.Encryption{Type: config.EncryptionStartTLS},
			Login:      "you@example.com",
			Auth: config.Auth{
				Type: config.AuthPassword,
				Raw:  value,
			},
		}}},
		Folder: &config.Folder{Aliases: map[string]string{"inbox": "INBOX"}},
	}
}

// oauthAccountNoWriteCmd builds an OAuth account whose secrets all resolve
// but whose refresh-token has no write_cmd — exercises Doctor's refusal to
// run the refresh probe.
func oauthAccountNoWriteCmd(t *testing.T) *config.Account {
	t.Helper()
	return &config.Account{
		Email: "you@gmail.com",
		Backend: config.Backend{
			Type:  config.BackendGmail,
			Login: "you@gmail.com",
			Auth: config.Auth{
				Type:         config.AuthOAuth2,
				ClientID:     "fake.apps.googleusercontent.com",
				AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL:     "https://oauth2.googleapis.com/token",
				ClientSecret: &config.Secret{Raw: "csec"},
				RefreshToken: &config.Secret{Raw: "rtok"}, // no WriteCmd
			},
		},
	}
}
