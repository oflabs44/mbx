// Package auth runs the three-legged OAuth2 authorization-code flow used
// by `mbx account auth`. Token persistence lives elsewhere — this package
// only obtains a token. Callers wire up secret.Write on the refresh-token.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/secret"
)

// DefaultAuthorizeTimeout bounds the flow when the caller's context has no
// deadline. Five minutes is long enough for a slow consent screen, short
// enough that a forgotten browser tab eventually frees the listener.
const DefaultAuthorizeTimeout = 5 * time.Minute

// Config builds an oauth2.Config from a parsed auth block. The client-secret
// secret is resolved at call time, which can execute the configured `cmd`.
// RedirectURL is left empty; Authorize fills it once the listener is bound.
func Config(ctx context.Context, a *config.Auth) (*oauth2.Config, error) {
	if a == nil {
		return nil, errors.New("auth block is nil")
	}
	if a.Type != config.AuthOAuth2 {
		return nil, fmt.Errorf("expected oauth2 auth, got %q", a.Type)
	}
	if a.ClientID == "" {
		return nil, errors.New("client-id is empty")
	}

	var clientSecret string
	if a.ClientSecret != nil {
		s, err := secret.Read(ctx, a.ClientSecret)
		if err != nil {
			return nil, fmt.Errorf("resolving client-secret: %w", err)
		}
		clientSecret = s
	}

	return &oauth2.Config{
		ClientID:     a.ClientID,
		ClientSecret: clientSecret,
		Scopes:       scopes(a),
		Endpoint: oauth2.Endpoint{
			AuthURL:  a.AuthURL,
			TokenURL: a.TokenURL,
		},
	}, nil
}

// scopes merges the singular `scope` and plural `scopes` config keys.
// `scopes` (list form) wins when both are set; the singular form is split
// on whitespace, the OAuth2 wire convention.
func scopes(a *config.Auth) []string {
	if len(a.Scopes) > 0 {
		return a.Scopes
	}
	if a.Scope == "" {
		return nil
	}
	return strings.Fields(a.Scope)
}

// AuthorizeOpts captures the per-flow knobs that aren't on oauth2.Config:
// the local redirect endpoint and PKCE toggle. OpenBrowser is injectable
// for tests; nil falls back to the OS-default opener.
type AuthorizeOpts struct {
	Scheme      string
	Host        string
	Port        int
	PKCE        bool
	OpenBrowser func(url string) error
}

// Authorize runs the flow end-to-end: binds a loopback listener, opens the
// user's browser to the consent screen, validates state, and exchanges the
// returned code for a token. The provided ctx bounds the wait; if it has
// no deadline, DefaultAuthorizeTimeout is applied.
func Authorize(ctx context.Context, cfg *oauth2.Config, opts AuthorizeOpts) (*oauth2.Token, error) {
	if cfg == nil {
		return nil, errors.New("oauth2 config is nil")
	}

	scheme, host := opts.Scheme, opts.Host
	if scheme == "" {
		scheme = "http"
	}
	if host == "" {
		host = "localhost"
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(opts.Port)))
	if err != nil {
		return nil, fmt.Errorf("binding callback listener on %s:%d: %w", host, opts.Port, err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	cfg.RedirectURL = fmt.Sprintf("%s://%s:%d/", scheme, host, port)

	state, err := randomState()
	if err != nil {
		return nil, fmt.Errorf("generating state: %w", err)
	}

	authOpts := []oauth2.AuthCodeOption{oauth2.AccessTypeOffline}
	var exchangeOpts []oauth2.AuthCodeOption
	if opts.PKCE {
		verifier := oauth2.GenerateVerifier()
		authOpts = append(authOpts, oauth2.S256ChallengeOption(verifier))
		exchangeOpts = append(exchangeOpts, oauth2.VerifierOption(verifier))
	}
	authURL := cfg.AuthCodeURL(state, authOpts...)

	open := opts.OpenBrowser
	if open == nil {
		open = openInBrowser
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultAuthorizeTimeout)
		defer cancel()
	}

	result := make(chan callbackResult, 1)
	server := &http.Server{
		Handler:      callbackHandler(state, result),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	// Opening the browser is best-effort: if `open` fails (no DISPLAY, no
	// default handler, headless server), surface the URL on stderr so the
	// user can paste it manually rather than aborting the flow. stdout is
	// reserved for the JSON envelope.
	if err := open(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "could not open browser (%v); paste this URL to authorize:\n%s\n", err, authURL)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("authorization timed out: %w", ctx.Err())
	case err := <-serveErr:
		return nil, fmt.Errorf("callback http server failed: %w", err)
	case cb := <-result:
		if cb.err != nil {
			return nil, cb.err
		}
		token, err := cfg.Exchange(ctx, cb.code, exchangeOpts...)
		if err != nil {
			return nil, fmt.Errorf("exchanging code for token: %w", err)
		}
		return token, nil
	}
}

type callbackResult struct {
	code string
	err  error
}

func callbackHandler(state string, out chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		if oerr := q.Get("error"); oerr != "" {
			writeCallbackHTML(w, "Authorization failed.", "Return to the terminal for details.")
			out <- callbackResult{err: providerError(q)}
			return
		}

		gotState := q.Get("state")
		if gotState != state {
			writeCallbackHTML(w, "State mismatch.", "Authorization request did not originate from this terminal.")
			out <- callbackResult{err: errors.New("state mismatch on oauth callback")}
			return
		}

		code := q.Get("code")
		if code == "" {
			writeCallbackHTML(w, "Missing authorization code.", "The provider did not include a code parameter.")
			out <- callbackResult{err: errors.New("missing code on oauth callback")}
			return
		}

		writeCallbackHTML(w, "Authorization complete.", "You can close this window and return to the terminal.")
		out <- callbackResult{code: code}
	})
}

func providerError(q url.Values) error {
	code := q.Get("error")
	if desc := q.Get("error_description"); desc != "" {
		return fmt.Errorf("oauth provider returned error %q: %s", code, desc)
	}
	return fmt.Errorf("oauth provider returned error %q", code)
}

func writeCallbackHTML(w http.ResponseWriter, heading, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w,
		`<!doctype html><html><head><meta charset="utf-8"><title>mbx</title></head>`+
			`<body style="font-family:system-ui,sans-serif;padding:48px;max-width:560px;margin:auto">`+
			`<h1>%s</h1><p>%s</p></body></html>`,
		heading, body)
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// openInBrowser delegates to the OS-native URL opener. Failure here doesn't
// abort the flow — Authorize falls back to printing the URL.
func openInBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}
