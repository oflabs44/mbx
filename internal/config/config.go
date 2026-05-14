// Package config loads and validates the mbx config file. The schema mirrors
// the himalaya-style TOML shape documented in docs/commands.md and ADR-0001.
//
// This package exposes typed structs and a Load function. Secret resolution
// (raw | keyring | cmd, plus write_cmd for OAuth refresh-token rotation)
// lives in internal/secret.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Accounts map[string]*Account `toml:"accounts"`
}

type Account struct {
	Type    AccountType `toml:"type"`
	Email   string      `toml:"email"`
	Backend Backend     `toml:"backend"`
	Send    *Send       `toml:"send,omitempty"`
	Cache   *Cache      `toml:"cache,omitempty"`
}

type AccountType string

const (
	AccountGmail AccountType = "gmail"
	AccountIMAP  AccountType = "imap"
)

type Backend struct {
	Host         string `toml:"host,omitempty"`
	Port         int    `toml:"port,omitempty"`
	TLS          string `toml:"tls,omitempty"`
	Auth         Auth   `toml:"auth"`
	ThreadWindow int    `toml:"thread_window,omitempty"`
}

type Send struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
	TLS  string `toml:"tls,omitempty"`
	Auth *Auth  `toml:"auth,omitempty"`
}

type Cache struct {
	Path     string   `toml:"path"`
	SyncDays int      `toml:"sync_days,omitempty"`
	Folders  []string `toml:"folders,omitempty"`
}

type AuthType string

const (
	AuthPassword AuthType = "password"
	AuthOAuth2   AuthType = "oauth2"
)

type Auth struct {
	Type     AuthType `toml:"type"`
	Username string   `toml:"username,omitempty"`

	Raw     string `toml:"raw,omitempty"`
	Keyring string `toml:"keyring,omitempty"`
	Cmd     string `toml:"cmd,omitempty"`

	ClientID       string   `toml:"client-id,omitempty"`
	ClientSecret   *Secret  `toml:"client-secret,omitempty"`
	AccessToken    *Secret  `toml:"access-token,omitempty"`
	RefreshToken   *Secret  `toml:"refresh-token,omitempty"`
	AuthURL        string   `toml:"auth-url,omitempty"`
	TokenURL       string   `toml:"token-url,omitempty"`
	Method         string   `toml:"method,omitempty"`
	PKCE           bool     `toml:"pkce,omitempty"`
	Scope          string   `toml:"scope,omitempty"`
	Scopes         []string `toml:"scopes,omitempty"`
	RedirectScheme string   `toml:"redirect-scheme,omitempty"`
	RedirectHost   string   `toml:"redirect-host,omitempty"`
	RedirectPort   int      `toml:"redirect-port,omitempty"`
}

// Secret is the himalaya-style tagged-sum: exactly one of Raw, Keyring, Cmd
// must be set. WriteCmd is meaningful only on rotating secrets (OAuth
// refresh-token) — validators enforce that contextually.
type Secret struct {
	Raw      string `toml:"raw,omitempty"`
	Keyring  string `toml:"keyring,omitempty"`
	Cmd      string `toml:"cmd,omitempty"`
	WriteCmd string `toml:"write_cmd,omitempty"`
}

type SecretVariant int

const (
	SecretNone SecretVariant = iota
	SecretRaw
	SecretKeyring
	SecretCmd
)

func (s *Secret) Variant() (SecretVariant, string) {
	switch {
	case s == nil:
		return SecretNone, ""
	case s.Raw != "":
		return SecretRaw, s.Raw
	case s.Keyring != "":
		return SecretKeyring, s.Keyring
	case s.Cmd != "":
		return SecretCmd, s.Cmd
	default:
		return SecretNone, ""
	}
}

func (s *Secret) HasWriteCmd() bool {
	return s != nil && s.WriteCmd != ""
}

// Sentinel errors. Mapping to stable output codes lives in cmd/mbx so this
// package stays free of output concerns (AGENTS §5).
var (
	ErrFileNotFound      = errors.New("config file not found")
	ErrInvalidTOML       = errors.New("invalid TOML")
	ErrMissingField      = errors.New("missing required field")
	ErrInvalidValue      = errors.New("invalid value")
	ErrAmbiguousSecret   = errors.New("ambiguous secret: set exactly one of raw, keyring, cmd")
	ErrMissingSecret     = errors.New("missing secret: set one of raw, keyring, cmd")
	ErrUnknownAccount    = errors.New("unknown account")
	ErrUnexpectedSection = errors.New("unexpected section")
)

// DefaultConfigDir returns the directory mbx looks in for its files when no
// explicit -c override is given. Resolution order:
//  1. $MBX_CONFIG_DIR — opt-in override for tests / multi-config workflows.
//  2. $XDG_CONFIG_HOME/mbx — the platform standard most CLI tools follow.
//  3. $HOME/.config/mbx — universal fallback. We do not use os.UserConfigDir
//     because on macOS it returns ~/Library/Application Support, which is
//     wrong for CLI tooling and conflicts with what docs/config.md documents.
func DefaultConfigDir() (string, error) {
	if v := os.Getenv("MBX_CONFIG_DIR"); v != "" {
		return expandHome(v), nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandHome(v), "mbx"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving user home dir: %w", err)
	}
	return filepath.Join(home, ".config", "mbx"), nil
}

// DefaultPath returns the config file path mbx loads when -c is not passed.
func DefaultPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load reads, decodes, and structurally validates the config at path.
// Empty path is rejected — callers should resolve a default via DefaultPath
// when no override is given.
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("config path is empty")
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrFileNotFound, path)
		}
		return nil, fmt.Errorf("opening config %s: %w", path, err)
	}
	defer f.Close()

	return decode(f, path)
}

func decode(r io.Reader, sourcePath string) (*Config, error) {
	dec := toml.NewDecoder(r)
	dec.DisallowUnknownFields()

	var c Config
	if err := dec.Decode(&c); err != nil {
		if de, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, col := de.Position()
			return nil, fmt.Errorf("%w: %s:%d:%d: %s", ErrInvalidTOML, sourcePath, row, col, de.Error())
		}
		if sm, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return nil, fmt.Errorf("%w: %s: %s", ErrInvalidTOML, sourcePath, sm.String())
		}
		return nil, fmt.Errorf("%w: %s: %s", ErrInvalidTOML, sourcePath, err.Error())
	}

	expandPaths(&c)

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Account looks up a named account. The boolean reports presence.
func (c *Config) Account(name string) (*Account, bool) {
	a, ok := c.Accounts[name]
	return a, ok
}

func (c *Config) validate() error {
	if len(c.Accounts) == 0 {
		return fmt.Errorf("%w: at least one [accounts.<name>] block required", ErrMissingField)
	}
	for name, a := range c.Accounts {
		if err := a.validate(); err != nil {
			return fmt.Errorf("account %q: %w", name, err)
		}
	}
	return nil
}

func (a *Account) validate() error {
	switch a.Type {
	case AccountGmail:
		if a.Send != nil {
			return fmt.Errorf("%w: gmail accounts must not have a [send] block (the Gmail API handles send)", ErrUnexpectedSection)
		}
		if err := a.Backend.Auth.validateOAuth2(); err != nil {
			return fmt.Errorf("backend.auth: %w", err)
		}
	case AccountIMAP:
		if a.Backend.Host == "" {
			return fmt.Errorf("%w: backend.host", ErrMissingField)
		}
		if a.Backend.Port == 0 {
			return fmt.Errorf("%w: backend.port", ErrMissingField)
		}
		if err := a.Backend.Auth.validate(); err != nil {
			return fmt.Errorf("backend.auth: %w", err)
		}
		if a.Send != nil {
			if a.Send.Host == "" {
				return fmt.Errorf("%w: send.host", ErrMissingField)
			}
			if a.Send.Port == 0 {
				return fmt.Errorf("%w: send.port", ErrMissingField)
			}
			if a.Send.Auth != nil {
				if err := a.Send.Auth.validate(); err != nil {
					return fmt.Errorf("send.auth: %w", err)
				}
			}
		}
	case "":
		return fmt.Errorf("%w: type (gmail | imap)", ErrMissingField)
	default:
		return fmt.Errorf("%w: type must be gmail or imap (got %q)", ErrInvalidValue, a.Type)
	}
	if a.Email == "" {
		return fmt.Errorf("%w: email", ErrMissingField)
	}
	if a.Cache != nil && a.Cache.Path == "" {
		return fmt.Errorf("%w: cache.path", ErrMissingField)
	}
	return nil
}

func (au *Auth) validate() error {
	switch au.Type {
	case AuthPassword:
		return au.validatePassword()
	case AuthOAuth2:
		return au.validateOAuth2()
	case "":
		return fmt.Errorf("%w: type (password | oauth2)", ErrMissingField)
	default:
		return fmt.Errorf("%w: auth.type must be password or oauth2 (got %q)", ErrInvalidValue, au.Type)
	}
}

func (au *Auth) validatePassword() error {
	switch countSet(au.Raw, au.Keyring, au.Cmd) {
	case 0:
		return fmt.Errorf("%w (password auth)", ErrMissingSecret)
	case 1:
		return nil
	default:
		return fmt.Errorf("%w (password auth)", ErrAmbiguousSecret)
	}
}

func (au *Auth) validateOAuth2() error {
	// Flat password-mode fields under an oauth2 block are a common migration
	// trap; reject loudly instead of silently ignoring.
	if au.Raw != "" || au.Keyring != "" || au.Cmd != "" {
		return fmt.Errorf("%w: flat raw/keyring/cmd belong to password auth, not oauth2", ErrUnexpectedSection)
	}
	if au.ClientID == "" {
		return fmt.Errorf("%w: client-id", ErrMissingField)
	}
	if au.AuthURL == "" {
		return fmt.Errorf("%w: auth-url", ErrMissingField)
	}
	if au.TokenURL == "" {
		return fmt.Errorf("%w: token-url", ErrMissingField)
	}
	if au.RefreshToken == nil {
		return fmt.Errorf("%w: refresh-token", ErrMissingField)
	}
	if err := au.RefreshToken.validate("refresh-token"); err != nil {
		return err
	}
	if au.ClientSecret != nil {
		if err := au.ClientSecret.validate("client-secret"); err != nil {
			return err
		}
	}
	if au.AccessToken != nil {
		if err := au.AccessToken.validate("access-token"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Secret) validate(name string) error {
	switch countSet(s.Raw, s.Keyring, s.Cmd) {
	case 0:
		return fmt.Errorf("%w (%s)", ErrMissingSecret, name)
	case 1:
		return nil
	default:
		return fmt.Errorf("%w (%s)", ErrAmbiguousSecret, name)
	}
}

func countSet(ss ...string) int {
	n := 0
	for _, s := range ss {
		if s != "" {
			n++
		}
	}
	return n
}

func expandPaths(c *Config) {
	for _, a := range c.Accounts {
		if a.Cache != nil {
			a.Cache.Path = expandHome(a.Cache.Path)
		}
	}
}

func expandHome(p string) string {
	if p == "" || !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}
