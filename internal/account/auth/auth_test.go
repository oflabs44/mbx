package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/oflabs44/mbx/internal/config"
)

func TestConfig_OAuth2(t *testing.T) {
	a := &config.Auth{
		Type:     config.AuthOAuth2,
		ClientID: "client-abc",
		ClientSecret: &config.Secret{
			Raw: "shh",
		},
		AuthURL:  "https://provider.example/auth",
		TokenURL: "https://provider.example/token",
		Scopes:   []string{"mail.read", "mail.send"},
	}

	cfg, err := Config(context.Background(), a)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.ClientID != "client-abc" || cfg.ClientSecret != "shh" {
		t.Errorf("client id/secret = %q/%q", cfg.ClientID, cfg.ClientSecret)
	}
	if cfg.Endpoint.AuthURL != a.AuthURL || cfg.Endpoint.TokenURL != a.TokenURL {
		t.Errorf("endpoint = %+v", cfg.Endpoint)
	}
	if !reflect.DeepEqual(cfg.Scopes, a.Scopes) {
		t.Errorf("scopes = %v", cfg.Scopes)
	}
}

func TestConfig_ScopesSingularSplits(t *testing.T) {
	a := &config.Auth{
		Type:     config.AuthOAuth2,
		ClientID: "x",
		AuthURL:  "https://p/auth", TokenURL: "https://p/token",
		ClientSecret: &config.Secret{Raw: "y"},
		Scope:        "a b c",
	}
	cfg, err := Config(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Scopes, []string{"a", "b", "c"}) {
		t.Errorf("scopes = %v", cfg.Scopes)
	}
}

func TestConfig_PluralWinsOverSingular(t *testing.T) {
	a := &config.Auth{
		Type:     config.AuthOAuth2,
		ClientID: "x",
		AuthURL:  "https://p/auth", TokenURL: "https://p/token",
		ClientSecret: &config.Secret{Raw: "y"},
		Scope:        "should-be-ignored",
		Scopes:       []string{"a", "b"},
	}
	cfg, _ := Config(context.Background(), a)
	if !reflect.DeepEqual(cfg.Scopes, []string{"a", "b"}) {
		t.Errorf("scopes = %v", cfg.Scopes)
	}
}

func TestConfig_RejectsWrongType(t *testing.T) {
	_, err := Config(context.Background(), &config.Auth{Type: config.AuthPassword})
	if err == nil {
		t.Fatal("want error for password auth, got nil")
	}
}

func TestAuthorize_HappyPath(t *testing.T) {
	tokenServer := newFakeTokenServer(t, "tok-1", "ref-1")
	defer tokenServer.Close()

	cfg := newFakeOAuthConfig(tokenServer.URL)

	browser := func(authURL string) error {
		go fakeBrowser(t, authURL, hijackState(authURL))
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tok, err := Authorize(ctx, cfg, AuthorizeOpts{OpenBrowser: browser})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if tok.AccessToken != "tok-1" || tok.RefreshToken != "ref-1" {
		t.Errorf("unexpected token: %+v", tok)
	}
}

func TestAuthorize_StateMismatch(t *testing.T) {
	tokenServer := newFakeTokenServer(t, "ignored", "")
	defer tokenServer.Close()
	cfg := newFakeOAuthConfig(tokenServer.URL)

	browser := func(authURL string) error {
		// Send a deliberately wrong state.
		go fakeBrowser(t, authURL, "bogus-state")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := Authorize(ctx, cfg, AuthorizeOpts{OpenBrowser: browser})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("want state mismatch error, got %v", err)
	}
}

func TestAuthorize_ProviderError(t *testing.T) {
	tokenServer := newFakeTokenServer(t, "ignored", "")
	defer tokenServer.Close()
	cfg := newFakeOAuthConfig(tokenServer.URL)

	browser := func(authURL string) error {
		go func() {
			u, _ := url.Parse(extractRedirect(authURL))
			q := u.Query()
			q.Set("error", "access_denied")
			q.Set("error_description", "user said no")
			u.RawQuery = q.Encode()
			_, _ = http.Get(u.String())
		}()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := Authorize(ctx, cfg, AuthorizeOpts{OpenBrowser: browser})
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("want provider error, got %v", err)
	}
}

func TestAuthorize_PKCEAttachesChallenge(t *testing.T) {
	tokenServer := newFakeTokenServer(t, "tok", "ref")
	defer tokenServer.Close()
	cfg := newFakeOAuthConfig(tokenServer.URL)

	var captured string
	browser := func(authURL string) error {
		captured = authURL
		go fakeBrowser(t, authURL, hijackState(authURL))
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := Authorize(ctx, cfg, AuthorizeOpts{PKCE: true, OpenBrowser: browser})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	u, _ := url.Parse(captured)
	if u.Query().Get("code_challenge") == "" {
		t.Errorf("auth URL missing code_challenge: %s", captured)
	}
	if got := u.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
}

// --- helpers ---

// newFakeOAuthConfig returns an oauth2.Config whose token endpoint points at
// the provided test server. AuthURL is unused (we synthesise the callback
// directly) but must be a parseable URL.
func newFakeOAuthConfig(tokenURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "client",
		ClientSecret: "secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://provider.example/auth",
			TokenURL: tokenURL,
		},
	}
}

// fakeBrowser hits the local callback listener exposed by Authorize with the
// supplied state. It infers the redirect URI from the auth URL the SUT
// would have built.
func fakeBrowser(t *testing.T, authURL, state string) {
	t.Helper()
	redirect := extractRedirect(authURL)
	u, err := url.Parse(redirect)
	if err != nil {
		t.Errorf("parse redirect: %v", err)
		return
	}
	q := u.Query()
	q.Set("code", "auth-code-xyz")
	q.Set("state", state)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		t.Errorf("callback GET: %v", err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func extractRedirect(authURL string) string {
	u, err := url.Parse(authURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("redirect_uri")
}

func hijackState(authURL string) string {
	u, _ := url.Parse(authURL)
	return u.Query().Get("state")
}

// newFakeTokenServer returns an httptest.Server that handles `/token`
// exchange requests with the given access and refresh tokens.
func newFakeTokenServer(t *testing.T, access, refresh string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"access_token":  access,
			"token_type":    "Bearer",
			"refresh_token": refresh,
			"expires_in":    3600,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}
