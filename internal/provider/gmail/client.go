package gmail

import (
	"context"
	"fmt"

	"golang.org/x/oauth2"
	gmailv1 "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"github.com/oflabs44/mbx/internal/account/auth"
	"github.com/oflabs44/mbx/internal/config"
)

// Client wraps a gmail.Service plus the account name it was built for so
// impl methods can stamp mbx IDs without being passed the name everywhere.
type Client struct {
	Account string
	Login   string
	svc     *gmailv1.Service
}

// New constructs a Gmail client for a configured account. The OAuth token
// source is the rotation-aware one in internal/account/auth — refresh-token
// rotation is mirrored back to the user's secret store transparently.
func New(ctx context.Context, name string, acct *config.Account) (*Client, error) {
	if acct.Backend.Type != config.BackendGmail {
		return nil, fmt.Errorf("gmail: account %q is %q backend, not gmail", name, acct.Backend.Type)
	}

	src, err := auth.TokenSource(ctx, &acct.Backend.Auth)
	if err != nil {
		return nil, fmt.Errorf("gmail: token source for %q: %w", name, err)
	}

	httpClient := oauth2.NewClient(ctx, src)
	svc, err := gmailv1.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("gmail: new service for %q: %w", name, err)
	}

	return &Client{Account: name, Login: acct.Backend.Login, svc: svc}, nil
}

// Probe makes a lightweight authenticated call to verify the token works
// and the configured login matches the account on the other end. Used by
// `account doctor` and as a smoke test from manual runs.
func (c *Client) Probe(ctx context.Context) (string, error) {
	prof, err := c.svc.Users.GetProfile("me").Context(ctx).Do()
	if err != nil {
		return "", mapErr(err)
	}
	return prof.EmailAddress, nil
}
