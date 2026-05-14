// Package smtp is the SMTP send backend used by IMAP accounts (Gmail
// uses its own HTTP API and does not go through here). It satisfies
// message.Sender; the connection is per-invocation, matching mbx's
// one-shot-per-command lifecycle.
package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"github.com/oflabs44/mbx/internal/account/auth"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/message"
	"github.com/oflabs44/mbx/internal/secret"
)

// Client wraps a *gosmtp.Client plus the account name + config it was
// built for. SMTP is send-only: there's no equivalent to the read-path
// envelope.Lister / message.Reader interfaces here.
type Client struct {
	Account string
	Cfg     *config.Account
	c       *gosmtp.Client
}

// New dials the configured SMTP server (under message.send.backend on
// the account), authenticates, and returns a ready-to-use client. Caller
// must defer Close() to issue QUIT and release the connection.
//
// The send block is required for IMAP accounts (config validation
// enforces this); Gmail accounts never go through here.
func New(ctx context.Context, name string, acct *config.Account) (*Client, error) {
	if acct.Message == nil || acct.Message.Send == nil {
		return nil, fmt.Errorf("smtp: account %q has no message.send.backend configured", name)
	}
	sb := &acct.Message.Send.Backend
	if sb.Type != config.BackendSMTP {
		return nil, fmt.Errorf("smtp: account %q message.send.backend type is %q, not smtp", name, sb.Type)
	}

	addr := net.JoinHostPort(sb.Host, strconv.Itoa(sb.Port))
	tlsConfig := &tls.Config{ServerName: sb.Host}
	if sb.Encryption != nil && sb.Encryption.Insecure {
		// Same opt-in escape hatch as the IMAP backend exposes for
		// loopback relays whose certs don't pass strict system trust.
		tlsConfig.InsecureSkipVerify = true
	}

	c, err := dial(addr, sb.Encryption, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("smtp: dial %s: %w", addr, err)
	}

	cli := &Client{Account: name, Cfg: acct, c: c}
	if err := cli.authenticate(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return cli, nil
}

func dial(addr string, enc *config.Encryption, tlsConfig *tls.Config) (*gosmtp.Client, error) {
	if enc == nil {
		return nil, errors.New("encryption.type is required")
	}
	switch enc.Type {
	case config.EncryptionTLS:
		return gosmtp.DialTLS(addr, tlsConfig)
	case config.EncryptionStartTLS:
		return gosmtp.DialStartTLS(addr, tlsConfig)
	case config.EncryptionNone:
		return gosmtp.Dial(addr)
	default:
		return nil, fmt.Errorf("unsupported encryption.type %q", enc.Type)
	}
}

// authenticate dispatches on message.send.backend.auth.type. Mirrors
// imap.Client.authenticate — password (PLAIN/LOGIN) or oauth2 (XOAUTH2).
// Inheritance from backend.auth.* is intentionally NOT performed (see
// docs/config.md): mbx never infers credentials across blocks.
//
// Refuses AUTH PLAIN over a cleartext socket (encryption.type=none):
// PLAIN base64s the password and ships it on the wire; combined with no
// TLS, the credentials leak. There's no opt-in escape hatch for this —
// move to start-tls / tls, or use oauth2.
func (c *Client) authenticate(ctx context.Context) error {
	a := &c.Cfg.Message.Send.Backend.Auth
	login := c.Cfg.Message.Send.Backend.Login
	switch a.Type {
	case config.AuthPassword:
		if enc := c.Cfg.Message.Send.Backend.Encryption; enc != nil && enc.Type == config.EncryptionNone {
			return fmt.Errorf("smtp: AUTH PLAIN over encryption.type=none would transmit the password in cleartext; configure encryption.type = \"start-tls\" or \"tls\"")
		}
		password, err := secret.Read(ctx, &config.Secret{Raw: a.Raw, Keyring: a.Keyring, Cmd: a.Cmd})
		if err != nil {
			return fmt.Errorf("smtp: resolving password: %w", err)
		}
		mech := sasl.NewPlainClient("", login, password)
		if err := c.c.Auth(mech); err != nil {
			return fmt.Errorf("smtp: AUTH PLAIN failed: %w", err)
		}
		return nil
	case config.AuthOAuth2:
		ts, err := auth.TokenSource(ctx, a)
		if err != nil {
			return fmt.Errorf("smtp: oauth2 token source: %w", err)
		}
		tok, err := ts.Token()
		if err != nil {
			return fmt.Errorf("smtp: obtaining oauth2 access token: %w", err)
		}
		return c.c.Auth(auth.NewXOAUTH2(login, tok.AccessToken))
	default:
		return fmt.Errorf("smtp: unsupported auth.type %q", a.Type)
	}
}

// SendMessage satisfies message.Sender. One MAIL FROM, one RCPT TO per
// recipient (To+Cc+Bcc deduped by the composer), one DATA carrying the
// RFC 5322 bytes. Bcc never appears in Raw — the composer strips it.
//
// The Mail/Rcpt/Data loop is hand-rolled rather than handed to
// go-smtp.SendMail so that an individual RCPT rejection surfaces the
// offending address: SendMail returns the bare protocol error and we
// can't tell the user which recipient broke the run.
func (c *Client) SendMessage(ctx context.Context, msg message.Outgoing) error {
	if msg.From == "" {
		return errors.New("smtp: outgoing message has empty From")
	}
	if len(msg.Recipients) == 0 {
		return errors.New("smtp: outgoing message has no recipients")
	}
	if err := c.c.Mail(msg.From, nil); err != nil {
		return fmt.Errorf("smtp: MAIL FROM %s: %w", msg.From, err)
	}
	for _, addr := range msg.Recipients {
		if err := c.c.Rcpt(addr, nil); err != nil {
			return fmt.Errorf("smtp: RCPT TO %s: %w", addr, err)
		}
	}
	w, err := c.c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := io.Copy(w, bytes.NewReader(msg.Raw)); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp: writing DATA: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: closing DATA (server rejected): %w", err)
	}
	return nil
}

// Close issues QUIT and tears down the connection. Safe to call once.
func (c *Client) Close() error {
	if c.c == nil {
		return nil
	}
	_ = c.c.Quit()
	return c.c.Close()
}
