package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/secret"
)

// rotationWriteTimeout caps the per-call deadline on the secret-store
// write that happens when the provider rotates the refresh token. The
// write must complete with a fresh context (not the caller's, which may
// be cancelled by Ctrl-C) — a partial rotation desyncs Gmail and the
// store. 30s is generous for any sane write_cmd; if it exceeds that,
// the user has bigger problems.
const rotationWriteTimeout = 30 * time.Second

// ErrRotationLost is returned when the OAuth provider issued a new
// refresh token but the persistence write failed. The old token is now
// dead on the provider; the user must re-run `mbx account auth`.
var ErrRotationLost = errors.New("oauth refresh token rotated but persistence via write_cmd failed; the stored token is now stale — re-run `mbx account auth`")

// TokenSource returns an oauth2.TokenSource for an OAuth2 auth block.
// The source seeds from the persisted refresh-token, refreshes on
// access-token expiry (oauth2.ReuseTokenSource semantics under the
// hood), and persists any rotated refresh token back via secret.Write
// before handing the token to the caller.
//
// Refuses if write_cmd is unset on the refresh-token: a rotation we
// can't persist would silently invalidate the user's stored credential.
func TokenSource(ctx context.Context, a *config.Auth) (oauth2.TokenSource, error) {
	if a == nil {
		return nil, errors.New("auth block is nil")
	}
	if a.Type != config.AuthOAuth2 {
		return nil, fmt.Errorf("expected oauth2 auth, got %q", a.Type)
	}
	if !a.RefreshToken.HasWriteCmd() {
		return nil, errors.New("refresh-token.write_cmd is unset; run `mbx account auth` first")
	}

	cfg, err := Config(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("building oauth2 config: %w", err)
	}
	refreshTok, err := secret.Read(ctx, a.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("reading refresh-token: %w", err)
	}

	base := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshTok})

	return &rotatingSource{
		base: base,
		sec:  a.RefreshToken,
		last: refreshTok,
	}, nil
}

// rotatingSource wraps an oauth2.TokenSource and mirrors any rotated
// refresh token back to secret.Write. Concurrent-safe.
//
// Note: oauth2.TokenSource.Token() takes no context, so we can't cache
// the caller's ctx. The persistence write deliberately uses a fresh
// context (rotationWriteTimeout) to avoid leaving the secret store and
// the OAuth provider out of sync if the caller's ctx was cancelled.
type rotatingSource struct {
	base oauth2.TokenSource
	sec  *config.Secret

	mu   sync.Mutex
	last string
}

func (r *rotatingSource) Token() (*oauth2.Token, error) {
	tok, err := r.base.Token()
	if err != nil {
		return nil, err
	}
	if tok.RefreshToken == "" {
		return tok, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if tok.RefreshToken == r.last {
		return tok, nil
	}

	writeCtx, cancel := context.WithTimeout(context.Background(), rotationWriteTimeout)
	defer cancel()
	if err := secret.Write(writeCtx, r.sec, tok.RefreshToken); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRotationLost, err)
	}
	r.last = tok.RefreshToken
	return tok, nil
}
