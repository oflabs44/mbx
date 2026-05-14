// Package mbxid parses and encodes the self-describing message IDs mbx
// emits in JSON output. See ADR-0002 for the rationale.
//
// Format:
//
//	gmail:<account>:<gmail-msg-id>
//	imap:<account>:<folder>:<uidvalidity>:<uid>
//
// The IMAP folder segment is percent-encoded for `:` (the separator) and
// `%` (the escape char) so that IMAP hierarchy (`INBOX/Sub`) and unusual
// folder names round-trip cleanly. Account names must not contain `:` or
// `%`; this is enforced at config-load time.
package mbxid

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Provider string

const (
	Gmail Provider = "gmail"
	IMAP  Provider = "imap"
)

type ID struct {
	Provider Provider
	Account  string

	GmailMsgID string

	Folder      string
	UIDValidity uint32
	UID         uint32
}

var (
	ErrInvalidFormat   = errors.New("invalid mbx ID format")
	ErrUnknownProvider = errors.New("unknown provider")
)

func NewGmail(account, msgID string) ID {
	return ID{Provider: Gmail, Account: account, GmailMsgID: msgID}
}

func NewIMAP(account, folder string, uidValidity, uid uint32) ID {
	return ID{Provider: IMAP, Account: account, Folder: folder, UIDValidity: uidValidity, UID: uid}
}

func (id ID) String() string {
	switch id.Provider {
	case Gmail:
		return fmt.Sprintf("%s:%s:%s", id.Provider, id.Account, id.GmailMsgID)
	case IMAP:
		return fmt.Sprintf("%s:%s:%s:%d:%d", id.Provider, id.Account, escape(id.Folder), id.UIDValidity, id.UID)
	default:
		return ""
	}
}

func Parse(s string) (ID, error) {
	if s == "" {
		return ID{}, fmt.Errorf("%w: empty", ErrInvalidFormat)
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) < 2 {
		return ID{}, fmt.Errorf("%w: missing provider separator", ErrInvalidFormat)
	}
	provider := Provider(parts[0])
	rest := parts[1]

	switch provider {
	case Gmail:
		return parseGmail(rest)
	case IMAP:
		return parseIMAP(rest)
	default:
		return ID{}, fmt.Errorf("%w: %q", ErrUnknownProvider, parts[0])
	}
}

func parseGmail(rest string) (ID, error) {
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ID{}, fmt.Errorf("%w: gmail needs account:gmail-msg-id", ErrInvalidFormat)
	}
	return ID{Provider: Gmail, Account: parts[0], GmailMsgID: parts[1]}, nil
}

func parseIMAP(rest string) (ID, error) {
	// Folder is percent-escaped on emit so any literal `:` becomes `%3A`,
	// which means the wire form under the imap prefix has exactly 4
	// colon-separated segments: account:folder:uidvalidity:uid.
	parts := strings.Split(rest, ":")
	if len(parts) != 4 {
		return ID{}, fmt.Errorf("%w: imap needs account:folder:uidvalidity:uid (got %d segments)", ErrInvalidFormat, len(parts))
	}
	account, folderRaw, uvStr, uidStr := parts[0], parts[1], parts[2], parts[3]
	if account == "" || folderRaw == "" || uvStr == "" || uidStr == "" {
		return ID{}, fmt.Errorf("%w: imap segment empty", ErrInvalidFormat)
	}
	folder, err := unescape(folderRaw)
	if err != nil {
		return ID{}, fmt.Errorf("%w: folder unescape: %v", ErrInvalidFormat, err)
	}
	uv, err := strconv.ParseUint(uvStr, 10, 32)
	if err != nil {
		return ID{}, fmt.Errorf("%w: uidvalidity: %v", ErrInvalidFormat, err)
	}
	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		return ID{}, fmt.Errorf("%w: uid: %v", ErrInvalidFormat, err)
	}
	return ID{Provider: IMAP, Account: account, Folder: folder, UIDValidity: uint32(uv), UID: uint32(uid)}, nil
}

var folderEscaper = strings.NewReplacer("%", "%25", ":", "%3A")

func escape(s string) string {
	return folderEscaper.Replace(s)
}

func unescape(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+3 > len(s) {
			return "", fmt.Errorf("truncated escape at %d", i)
		}
		switch s[i : i+3] {
		case "%3A":
			b.WriteByte(':')
		case "%25":
			b.WriteByte('%')
		default:
			return "", fmt.Errorf("unrecognized escape %q at %d", s[i:i+3], i)
		}
		i += 3
	}
	return b.String(), nil
}
