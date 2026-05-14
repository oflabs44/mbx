package envelope

import (
	"context"
	"fmt"
	"strings"

	"github.com/oflabs44/mbx/internal/mbxid"
)

// Flag is the normalized mbx flag vocabulary (CONTEXT.md). Backends
// translate to provider-native names; see provider/<name>/flag.go. The
// positive form is canonical — Gmail's negative UNREAD inverts at the
// gmail backend boundary.
type Flag string

const (
	FlagSeen     Flag = "seen"
	FlagFlagged  Flag = "flagged"
	FlagAnswered Flag = "answered"
	FlagDraft    Flag = "draft"
	FlagDeleted  Flag = "deleted"
)

// AllFlags is the full vocabulary in stable order.
var AllFlags = []Flag{FlagSeen, FlagFlagged, FlagAnswered, FlagDraft, FlagDeleted}

// ParseFlag validates a user-typed name (case-sensitive — the wire form
// is lowercase and stable) against the vocabulary.
func ParseFlag(s string) (Flag, error) {
	for _, f := range AllFlags {
		if string(f) == s {
			return f, nil
		}
	}
	names := make([]string, len(AllFlags))
	for i, f := range AllFlags {
		names[i] = string(f)
	}
	return "", fmt.Errorf("unknown flag %q (allowed: %s)", s, strings.Join(names, ", "))
}

// Flagger is the narrow consumer interface ApplyFlags requires. Backends
// satisfy it by writing a matching method (Go interface idiom: defined
// at the consumer).
type Flagger interface {
	FlagEnvelopes(ctx context.Context, ids []mbxid.ID, add, remove []Flag) error
}

// ApplyFlags is the domain entry point for `mbx envelope flag`. Kept for
// symmetry with envelope.List / envelope.Search; future cross-backend
// bounds land here.
func ApplyFlags(ctx context.Context, f Flagger, ids []mbxid.ID, add, remove []Flag) error {
	return f.FlagEnvelopes(ctx, ids, add, remove)
}
