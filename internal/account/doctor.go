package account

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/oauth2"

	"github.com/oflabs44/mbx/internal/account/auth"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/secret"
)

// Status is one slot's outcome in a DoctorReport. Surfaced as the slot's
// string value in the JSON envelope.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// DoctorReport is the payload of `mbx account doctor`. Each slot reports a
// Status; Errors keys the same slot names and carries human-readable detail
// when a slot is in fail / warn. Warnings is for non-slot-bound notes
// (e.g., "connectivity probe not yet implemented").
type DoctorReport struct {
	Account      string            `json:"account"`
	Config       Status            `json:"config"`
	Secrets      Status            `json:"secrets"`
	Auth         Status            `json:"auth"`
	Connectivity Status            `json:"connectivity"`
	Capabilities map[string]any    `json:"capabilities"`
	Warnings     []string          `json:"warnings,omitempty"`
	Errors       map[string]string `json:"errors,omitempty"`
}

// Doctor probes a configured account end-to-end (less the provider-level
// connectivity and capability checks, which land with Phase 2 / Phase 3).
//
// Failure semantics: ErrUnknownAccount is returned as an error so the
// caller can map it to a structured `config.unknown_account` envelope on
// stderr. All other failure modes appear as slot statuses on the returned
// report; the function itself returns nil error in those cases. Doctor is
// a reporter, not a gate — its job is to surface state, not to fail the
// command.
func Doctor(ctx context.Context, c *config.Config, name string) (*DoctorReport, error) {
	cname, acct, err := Lookup(c, name)
	if err != nil {
		return nil, err
	}

	report := &DoctorReport{
		Account:      cname,
		Config:       StatusOK,
		Secrets:      StatusOK,
		Auth:         StatusOK,
		Connectivity: StatusWarn,
		Warnings: []string{
			"connectivity probe lands in phase 2 (gmail) / phase 3 (imap)",
			"capability detection lands in phase 2 (gmail) / phase 3 (imap)",
		},
	}

	if err := checkAccountSecrets(ctx, acct); err != nil {
		report.Secrets = StatusFail
		report.Errors = map[string]string{"secrets": err.Error()}
		report.Auth = StatusSkip
		return report, nil
	}

	if acct.Backend.Auth.Type == config.AuthOAuth2 {
		if err := probeOAuth(ctx, &acct.Backend.Auth); err != nil {
			report.Auth = StatusFail
			report.Errors = map[string]string{"auth": err.Error()}
		}
	}

	return report, nil
}

// checkAccountSecrets resolves every secret reachable from the account: the
// backend auth, and the send backend's auth when present. Stops at the
// first failure so the report names a specific slot to fix.
func checkAccountSecrets(ctx context.Context, a *config.Account) error {
	if err := checkAuthSecrets(ctx, &a.Backend.Auth, "backend.auth"); err != nil {
		return err
	}
	if a.Message != nil && a.Message.Send != nil {
		if err := checkAuthSecrets(ctx, &a.Message.Send.Backend.Auth, "message.send.backend.auth"); err != nil {
			return err
		}
	}
	return nil
}

func checkAuthSecrets(ctx context.Context, a *config.Auth, prefix string) error {
	switch a.Type {
	case config.AuthPassword:
		s := &config.Secret{Raw: a.Raw, Keyring: a.Keyring, Cmd: a.Cmd}
		if _, err := secret.Read(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	case config.AuthOAuth2:
		if a.ClientSecret != nil {
			if _, err := secret.Read(ctx, a.ClientSecret); err != nil {
				return fmt.Errorf("%s.client-secret: %w", prefix, err)
			}
		}
		if a.RefreshToken != nil {
			if _, err := secret.Read(ctx, a.RefreshToken); err != nil {
				return fmt.Errorf("%s.refresh-token: %w", prefix, err)
			}
		}
		if a.AccessToken != nil {
			if _, err := secret.Read(ctx, a.AccessToken); err != nil {
				return fmt.Errorf("%s.access-token: %w", prefix, err)
			}
		}
	}
	return nil
}

// probeOAuth exercises the refresh path using the stored refresh-token. If
// the provider rotates it during the exchange and write_cmd is available,
// the new value is persisted — same contract as `account auth`. Without
// write_cmd we refuse to refresh, because a rotation here would silently
// invalidate the stored token.
func probeOAuth(ctx context.Context, a *config.Auth) error {
	if !a.RefreshToken.HasWriteCmd() {
		return fmt.Errorf("backend.auth.refresh-token.write_cmd is unset; run `mbx account auth` first")
	}

	cfg, err := auth.Config(ctx, a)
	if err != nil {
		return fmt.Errorf("building oauth2 config: %w", err)
	}
	refreshTok, err := secret.Read(ctx, a.RefreshToken)
	if err != nil {
		return fmt.Errorf("reading refresh-token: %w", err)
	}

	tok := &oauth2.Token{
		RefreshToken: refreshTok,
		Expiry:       time.Now().Add(-time.Hour),
	}
	newTok, err := cfg.TokenSource(ctx, tok).Token()
	if err != nil {
		return fmt.Errorf("refresh failed: %w", err)
	}

	if newTok.RefreshToken != "" && newTok.RefreshToken != refreshTok {
		if err := secret.Write(ctx, a.RefreshToken, newTok.RefreshToken); err != nil {
			return fmt.Errorf("persisting rotated refresh-token: %w", err)
		}
	}
	return nil
}
