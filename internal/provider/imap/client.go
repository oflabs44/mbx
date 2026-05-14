// Package imap is the IMAP backend. It satisfies the same narrow
// consumer interfaces internal/provider/gmail satisfies — envelope.Lister,
// message.Reader/RawReader, folder.Lister, attachment.Lister/Downloader —
// against any IMAP server (Proton bridge, Migadu, corporate Exchange, …).
//
// Connection lifecycle is per-invocation: New dials and authenticates;
// Close logs out and tears down. mbx is one-shot per command, so a single
// connection per Client lifetime is the right granularity.
package imap

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/oflabs44/mbx/internal/account/auth"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/output"
	"github.com/oflabs44/mbx/internal/secret"
)

// Client wraps an imapclient.Client plus the account name + config it
// was built for so impl methods can stamp mbx IDs and resolve folder
// aliases without being passed the name everywhere.
type Client struct {
	Account string
	Cfg     *config.Account
	c       *imapclient.Client
}

// New dials the configured IMAP server, authenticates, and returns a
// ready-to-use client. The returned Client owns the underlying TCP
// connection — call Close when done to issue LOGOUT and release it.
//
// TLS verification follows the system trust store; mbx does not expose
// an insecure-skip-verify knob (Proton bridge users add the bridge cert
// to their system store, same as any IMAP client).
func New(ctx context.Context, name string, acct *config.Account) (*Client, error) {
	if acct.Backend.Type != config.BackendIMAP {
		return nil, fmt.Errorf("imap: account %q is %q backend, not imap", name, acct.Backend.Type)
	}

	addr := net.JoinHostPort(acct.Backend.Host, strconv.Itoa(acct.Backend.Port))
	tlsConfig := &tls.Config{ServerName: acct.Backend.Host}
	if acct.Backend.Encryption != nil && acct.Backend.Encryption.Insecure {
		// Per docs/config.md: opt-in escape hatch for loopback relays
		// (Proton bridge, dev-only IMAP) whose certs don't pass strict
		// system trust validation. Documented as such and defaults off.
		tlsConfig.InsecureSkipVerify = true
	}
	opts := &imapclient.Options{TLSConfig: tlsConfig}

	c, err := dial(addr, acct.Backend.Encryption, opts)
	if err != nil {
		return nil, fmt.Errorf("imap: dial %s: %w", addr, err)
	}

	cli := &Client{Account: name, Cfg: acct, c: c}
	if err := cli.authenticate(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}

	return cli, nil
}

// dial issues the right Dial* per encryption type. The "none" variant
// (DialInsecure) sends credentials in the clear; valid for some local
// dev setups but never appropriate for a remote server. Config validation
// in internal/config already gates this on an explicit user choice.
func dial(addr string, enc *config.Encryption, opts *imapclient.Options) (*imapclient.Client, error) {
	if enc == nil {
		return nil, errors.New("encryption.type is required")
	}
	switch enc.Type {
	case config.EncryptionTLS:
		return imapclient.DialTLS(addr, opts)
	case config.EncryptionStartTLS:
		return imapclient.DialStartTLS(addr, opts)
	case config.EncryptionNone:
		return imapclient.DialInsecure(addr, opts)
	default:
		return nil, fmt.Errorf("unsupported encryption.type %q", enc.Type)
	}
}

// authenticate dispatches on the account's auth.type. Phase-3.1 lands
// password auth (LOGIN); XOAUTH2 SASL ships in 3.6.
func (c *Client) authenticate(ctx context.Context) error {
	a := &c.Cfg.Backend.Auth
	switch a.Type {
	case config.AuthPassword:
		password, err := readPasswordSecret(ctx, a)
		if err != nil {
			return fmt.Errorf("imap: resolving password: %w", err)
		}
		if err := c.c.Login(c.Cfg.Backend.Login, password).Wait(); err != nil {
			return fmt.Errorf("imap: LOGIN failed: %w", err)
		}
		return nil
	case config.AuthOAuth2:
		ts, err := auth.TokenSource(ctx, a)
		if err != nil {
			return fmt.Errorf("imap: oauth2 token source: %w", err)
		}
		tok, err := ts.Token()
		if err != nil {
			return fmt.Errorf("imap: obtaining oauth2 access token: %w", err)
		}
		return c.c.Authenticate(newXOAUTH2(c.Cfg.Backend.Login, tok.AccessToken))
	default:
		return fmt.Errorf("imap: unsupported auth.type %q", a.Type)
	}
}

// readPasswordSecret resolves a password-auth secret from the flat
// raw|keyring|cmd fields on the auth block.
func readPasswordSecret(ctx context.Context, a *config.Auth) (string, error) {
	return secret.Read(ctx, &config.Secret{Raw: a.Raw, Keyring: a.Keyring, Cmd: a.Cmd})
}

// Close logs out and releases the connection. Safe to call once.
func (c *Client) Close() error {
	if c.c == nil {
		return nil
	}
	_ = c.c.Logout().Wait()
	return c.c.Close()
}

// Probe issues NOOP. Used as a connectivity smoke test (e.g. by
// `account doctor` once IMAP is wired into it).
func (c *Client) Probe(ctx context.Context) error {
	return c.c.Noop().Wait()
}

// assertOwns rejects mbx IDs that aren't IMAP or that belong to a
// different account on this client.
func (c *Client) assertOwns(id mbxid.ID) error {
	if id.Provider != mbxid.IMAP {
		return fmt.Errorf("imap: id %q is not an imap id", id.String())
	}
	if id.Account != c.Account {
		return fmt.Errorf("imap: id account %q does not match client %q", id.Account, c.Account)
	}
	return nil
}

// selectAndVerify SELECTs the folder embedded in the mbx ID and
// confirms the server's UIDVALIDITY matches the value the ID was
// minted with. Mismatch surfaces as a stable provider.id_invalidated
// error code (exit 22) — the caller's stored ID is no longer addressable.
func (c *Client) selectAndVerify(id mbxid.ID) error {
	sel, err := c.c.Select(id.Folder, nil).Wait()
	if err != nil {
		return fmt.Errorf("imap: SELECT %q: %w", id.Folder, err)
	}
	if sel.UIDValidity != id.UIDValidity {
		return output.Errorf(output.CodeProviderIDInvalidated,
			"folder %q UIDVALIDITY changed (id was minted at %d, server is %d); re-list to get fresh ids",
			id.Folder, id.UIDValidity, sel.UIDValidity).
			WithDetails("folder", id.Folder).
			WithDetails("id", id.String())
	}
	return nil
}
