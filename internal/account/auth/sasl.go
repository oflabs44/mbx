package auth

import (
	"errors"
	"fmt"

	"github.com/emersion/go-sasl"
)

// xoauth2Mechanism is the SASL mech name as registered by Google /
// Microsoft / Yahoo for OAuth2 IMAP and SMTP authentication.
const xoauth2Mechanism = "XOAUTH2"

// NewXOAUTH2 constructs a sasl.Client for the XOAUTH2 mechanism, suitable
// for handing to imapclient.Client.Authenticate or smtp.Client.Auth.
// Spec: https://developers.google.com/gmail/imap/xoauth2-protocol — the
// initial response is a single base64-encoded string, no challenges are
// expected on success. On auth failure the server sends a JSON error
// blob as a continuation, which we surface verbatim so the user can see
// what the server objected to.
func NewXOAUTH2(username, accessToken string) sasl.Client {
	return &xoauth2{username: username, token: accessToken}
}

type xoauth2 struct {
	username string
	token    string
}

func (x *xoauth2) Start() (mech string, ir []byte, err error) {
	if x.username == "" || x.token == "" {
		return "", nil, errors.New("xoauth2: username and access token are required")
	}
	return xoauth2Mechanism, formatXOAUTH2(x.username, x.token), nil
}

func (x *xoauth2) Next(challenge []byte) ([]byte, error) {
	return nil, fmt.Errorf("xoauth2: server rejected authentication: %s", string(challenge))
}

// formatXOAUTH2 builds the SASL initial response per the spec:
//
//	user=<username>^Aauth=Bearer <token>^A^A
//
// where ^A is the literal control character 0x01.
func formatXOAUTH2(username, token string) []byte {
	return []byte("user=" + username + "\x01auth=Bearer " + token + "\x01\x01")
}
