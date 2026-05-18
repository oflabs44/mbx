// Package config loads and validates the mbx config file. The TOML schema
// is documented in docs/config.md and decided in ADR-0006 (himalaya-aligned
// dotted-key shape with mbx-specific extensions).
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

// Top-level config. Globals appear as flat keys; per-account configuration
// lives under [accounts.<name>].
type Config struct {
	DownloadsDir string              `toml:"downloads-dir,omitempty"`
	CacheDir     string              `toml:"cache-dir,omitempty"`
	Accounts     map[string]*Account `toml:"accounts"`

	// aliasToCanon is built in validate(): each entry maps an alias to
	// the canonical account name it resolves to. Collisions are rejected
	// at load time so a lookup never has to disambiguate. See ADR-0007.
	aliasToCanon map[string]string `toml:"-"`
}

type Account struct {
	Email   string   `toml:"email"`
	Aliases []string `toml:"aliases,omitempty"`
	Backend Backend  `toml:"backend"`
	Message *Message `toml:"message,omitempty"`
	Folder  *Folder  `toml:"folder,omitempty"`
	Cache   *Cache   `toml:"cache,omitempty"`
}

// Backend models both the account's read backend and the message.send.backend.
// Type discriminates the two contexts ([imap|gmail] for read; [smtp] for send),
// and validation enforces context-appropriate field requirements.
type Backend struct {
	Type         BackendType `toml:"type"`
	Host         string      `toml:"host,omitempty"`
	Port         int         `toml:"port,omitempty"`
	Encryption   *Encryption `toml:"encryption,omitempty"`
	Login        string      `toml:"login,omitempty"`
	Auth         Auth        `toml:"auth"`
	Extensions   *Extensions `toml:"extensions,omitempty"`
	ThreadWindow int         `toml:"thread_window,omitempty"`
}

type BackendType string

const (
	BackendIMAP  BackendType = "imap"
	BackendGmail BackendType = "gmail"
	BackendSMTP  BackendType = "smtp"
)

type Encryption struct {
	Type string `toml:"type"`
	// Insecure disables TLS certificate verification. Intended for
	// loopback relays (Proton bridge, dev-only IMAP) that ship self-
	// signed or non-Apple-compliant certs. Defaults to false; setting
	// it on a remote host turns the connection into a downgrade target.
	Insecure bool `toml:"insecure,omitempty"`
}

const (
	EncryptionTLS      = "tls"
	EncryptionStartTLS = "start-tls"
	EncryptionNone     = "none"
)

type Message struct {
	Send   *MessageSend   `toml:"send,omitempty"`
	Delete *MessageDelete `toml:"delete,omitempty"`
}

type MessageSend struct {
	Backend  Backend `toml:"backend"`
	SaveCopy bool    `toml:"save-copy,omitempty"`
	PreHook  string  `toml:"pre-hook,omitempty"`
}

type MessageDelete struct {
	Style string `toml:"style,omitempty"` // "flag" | "folder"
}

type Folder struct {
	Aliases map[string]string `toml:"aliases,omitempty"`
}

type Cache struct {
	SyncDays int      `toml:"sync_days,omitempty"`
	Folders  []string `toml:"folders,omitempty"`
}

type AuthType string

const (
	AuthPassword AuthType = "password"
	AuthOAuth2   AuthType = "oauth2"
)

type Auth struct {
	Type AuthType `toml:"type"`

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

type Extensions struct {
	ID *IDExtension `toml:"id,omitempty"`
}

type IDExtension struct {
	SendAfterAuth bool `toml:"send_after_auth,omitempty"`
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
			msg := sm.String()
			// Per ADR-0008, cache.path is gone; surface a hint that
			// points the user at the global cache-dir.
			if strings.Contains(msg, `"path"`) && strings.Contains(msg, "Cache") {
				return nil, fmt.Errorf("%w: %s: per-account cache.path is no longer supported; set the top-level `cache-dir` instead (ADR-0008). %s",
					ErrInvalidTOML, sourcePath, msg)
			}
			return nil, fmt.Errorf("%w: %s: %s", ErrInvalidTOML, sourcePath, msg)
		}
		return nil, fmt.Errorf("%w: %s: %s", ErrInvalidTOML, sourcePath, err.Error())
	}

	expandPaths(&c)

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Resolve looks up an account by its canonical name or any of its aliases.
// Returns the canonical name (so callers stamping new mbx IDs use the
// stable form), the account, and ok=true on a hit. See ADR-0007.
func (c *Config) Resolve(name string) (canonical string, acct *Account, ok bool) {
	if a, hit := c.Accounts[name]; hit {
		return name, a, true
	}
	if cname, hit := c.aliasToCanon[name]; hit {
		return cname, c.Accounts[cname], true
	}
	return "", nil, false
}

// ReservedAccountName is the `-a` sentinel for fanout-to-every-account; an
// account or alias named "all" would shadow it at runtime, so reject at load.
const ReservedAccountName = "all"

func (c *Config) validate() error {
	if len(c.Accounts) == 0 {
		return fmt.Errorf("%w: at least one [accounts.<name>] block required", ErrMissingField)
	}
	if _, taken := c.Accounts[ReservedAccountName]; taken {
		return fmt.Errorf("%w: account name %q is reserved (used by -a all to fan out across every account)",
			ErrInvalidValue, ReservedAccountName)
	}
	for name, a := range c.Accounts {
		if err := a.validate(); err != nil {
			return fmt.Errorf("account %q: %w", name, err)
		}
	}
	return c.BuildAliasIndex()
}

// BuildAliasIndex populates aliasToCanon and rejects collisions: an alias
// matching another account's canonical name, or two accounts claiming the
// same alias. Detecting at load means a downstream Resolve never has to
// disambiguate.
//
// Load runs this automatically; the export exists for tests that
// construct Config literals.
func (c *Config) BuildAliasIndex() error {
	c.aliasToCanon = map[string]string{}
	for cname, a := range c.Accounts {
		for _, alias := range a.Aliases {
			if alias == "" {
				return fmt.Errorf("%w: account %q has an empty alias", ErrInvalidValue, cname)
			}
			if alias == cname {
				return fmt.Errorf("%w: account %q lists itself as an alias", ErrInvalidValue, cname)
			}
			if alias == ReservedAccountName {
				return fmt.Errorf("%w: alias %q on account %q is reserved (used by -a all to fan out across every account)",
					ErrInvalidValue, alias, cname)
			}
			if _, isCanonical := c.Accounts[alias]; isCanonical {
				return fmt.Errorf("%w: alias %q on account %q collides with the canonical name of another account",
					ErrInvalidValue, alias, cname)
			}
			if prior, taken := c.aliasToCanon[alias]; taken {
				return fmt.Errorf("%w: alias %q claimed by both %q and %q",
					ErrInvalidValue, alias, prior, cname)
			}
			c.aliasToCanon[alias] = cname
		}
	}
	return nil
}

func (a *Account) validate() error {
	if a.Email == "" {
		return fmt.Errorf("%w: email", ErrMissingField)
	}
	if err := a.Backend.validateAsRead(); err != nil {
		return fmt.Errorf("backend: %w", err)
	}

	switch a.Backend.Type {
	case BackendIMAP:
		if a.Message == nil || a.Message.Send == nil {
			return fmt.Errorf("%w: message.send.backend (required for imap accounts)", ErrMissingField)
		}
		if err := a.Message.Send.Backend.validateAsSend(); err != nil {
			return fmt.Errorf("message.send.backend: %w", err)
		}
		if a.Folder == nil || a.Folder.Aliases["inbox"] == "" {
			return fmt.Errorf("%w: folder.aliases.inbox (required for imap accounts)", ErrMissingField)
		}
	case BackendGmail:
		if a.Message != nil && a.Message.Send != nil {
			return fmt.Errorf("%w: message.send.backend (forbidden for gmail accounts — the Gmail API handles send)", ErrUnexpectedSection)
		}
	}

	return nil
}

// validateAsRead validates a backend in the read-side context: it must be
// imap or gmail (smtp is rejected here).
func (b *Backend) validateAsRead() error {
	switch b.Type {
	case BackendIMAP:
		return b.validateNetworkFields()
	case BackendGmail:
		if b.Host != "" || b.Port != 0 || b.Encryption != nil {
			return fmt.Errorf("%w: gmail backends use the Gmail HTTP API; host, port, and encryption are not configurable", ErrUnexpectedSection)
		}
		if b.Login == "" {
			return fmt.Errorf("%w: login", ErrMissingField)
		}
		if err := b.Auth.validate(); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
		if b.Auth.Type != AuthOAuth2 {
			return fmt.Errorf("%w: gmail backends require auth.type = \"oauth2\"", ErrInvalidValue)
		}
		return nil
	case BackendSMTP:
		return fmt.Errorf("%w: type %q is only valid under message.send.backend", ErrInvalidValue, b.Type)
	case "":
		return fmt.Errorf("%w: type (imap | gmail)", ErrMissingField)
	default:
		return fmt.Errorf("%w: type must be imap or gmail (got %q)", ErrInvalidValue, b.Type)
	}
}

// validateAsSend validates a backend in the send-side context: currently
// smtp only.
func (b *Backend) validateAsSend() error {
	switch b.Type {
	case BackendSMTP:
		return b.validateNetworkFields()
	case "":
		return fmt.Errorf("%w: type (smtp)", ErrMissingField)
	default:
		return fmt.Errorf("%w: message.send.backend.type must be smtp (got %q)", ErrInvalidValue, b.Type)
	}
}

// validateNetworkFields enforces the IMAP / SMTP shared shape: host, port,
// encryption, login, auth.
func (b *Backend) validateNetworkFields() error {
	if b.Host == "" {
		return fmt.Errorf("%w: host", ErrMissingField)
	}
	if b.Port == 0 {
		return fmt.Errorf("%w: port", ErrMissingField)
	}
	if b.Encryption == nil || b.Encryption.Type == "" {
		return fmt.Errorf("%w: encryption.type", ErrMissingField)
	}
	switch b.Encryption.Type {
	case EncryptionTLS, EncryptionStartTLS, EncryptionNone:
	default:
		return fmt.Errorf("%w: encryption.type must be tls, start-tls, or none (got %q)", ErrInvalidValue, b.Encryption.Type)
	}
	if b.Encryption.Insecure && b.Encryption.Type == EncryptionNone {
		return fmt.Errorf("%w: encryption.insecure=true makes no sense when encryption.type=none", ErrInvalidValue)
	}
	if b.Login == "" {
		return fmt.Errorf("%w: login", ErrMissingField)
	}
	if err := b.Auth.validate(); err != nil {
		return fmt.Errorf("auth: %w", err)
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
	if au.ClientID != "" || au.AuthURL != "" || au.TokenURL != "" ||
		au.RefreshToken != nil || au.ClientSecret != nil || au.AccessToken != nil {
		return fmt.Errorf("%w: oauth2 fields under a password auth block", ErrUnexpectedSection)
	}
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
	c.DownloadsDir = expandHome(c.DownloadsDir)
	c.CacheDir = expandHome(c.CacheDir)
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
