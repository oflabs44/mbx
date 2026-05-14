package message

import "context"

// Sender is the narrow consumer interface `mbx message send` requires.
// Backends satisfy it by writing a matching method; declared here, next
// to its consumer (Go interface idiom).
//
// Gmail satisfies it via the Gmail HTTP API (users.messages.send and the
// raw bytes); SMTP satisfies it by speaking the protocol.
type Sender interface {
	SendMessage(ctx context.Context, msg Outgoing) error
}

// Send is the domain entry point for the send verb. Kept for parity with
// the other write-path verbs; future cross-backend bounds (rate limiting,
// hooks) land here.
func Send(ctx context.Context, s Sender, msg Outgoing) error {
	return s.SendMessage(ctx, msg)
}
