package gmail

import (
	"context"
	"encoding/base64"
	"strings"

	gmailv1 "google.golang.org/api/gmail/v1"

	"github.com/oflabs44/mbx/internal/message"
)

// SendMessage satisfies message.Sender. Gmail's users.messages.send
// determines delivery recipients by parsing the wire body's headers
// (Outgoing.Recipients is unused here). That means Bcc, which the
// composer keeps out of Outgoing.Raw, has to be reinjected as a header
// before sending — otherwise Gmail never sees the Bcc addresses and
// silently drops them.
//
// Gmail's documented behaviour: it strips the Bcc header before
// delivery, so adding it here is invisible to recipients.
func (c *Client) SendMessage(ctx context.Context, msg message.Outgoing) error {
	raw := injectBccHeader(msg.Raw, msg.Bcc)
	req := &gmailv1.Message{Raw: base64.URLEncoding.EncodeToString(raw)}
	_, err := c.svc.Users.Messages.Send("me", req).Context(ctx).Do()
	return mapErr(err)
}

// injectBccHeader prepends a Bcc: line to the RFC 5322 bytes. The header
// block ends at the first empty line — prepending is safe because
// headers may appear in any order and "Bcc:" before the rest still
// parses. Returns the original bytes unchanged when bcc is empty so the
// common no-bcc path stays zero-allocation.
func injectBccHeader(raw []byte, bcc []string) []byte {
	if len(bcc) == 0 {
		return raw
	}
	header := "Bcc: " + strings.Join(bcc, ", ") + "\r\n"
	out := make([]byte, 0, len(header)+len(raw))
	out = append(out, header...)
	out = append(out, raw...)
	return out
}
