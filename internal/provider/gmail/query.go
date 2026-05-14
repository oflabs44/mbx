package gmail

import (
	"strings"

	"github.com/oflabs44/mbx/internal/envelope"
)

// buildQuery assembles a Gmail query string from a normalized ListQuery.
// Filters compose with implicit AND. RawQuery is appended verbatim — that's
// the --query escape hatch documented in commands.md.
//
// Folder is intentionally NOT folded in here; the SDK takes label-id as a
// separate field on Users.Messages.List.
func buildQuery(q envelope.ListQuery) string {
	parts := make([]string, 0, 8)

	if q.Unread != nil {
		if *q.Unread {
			parts = append(parts, "is:unread")
		} else {
			parts = append(parts, "is:read")
		}
	}
	if q.Starred != nil && *q.Starred {
		parts = append(parts, "is:starred")
	}
	if q.HasAttachment != nil && *q.HasAttachment {
		parts = append(parts, "has:attachment")
	}
	if q.From != "" {
		parts = append(parts, "from:"+quoteIfNeeded(q.From))
	}
	if q.To != "" {
		parts = append(parts, "to:"+quoteIfNeeded(q.To))
	}
	if !q.After.IsZero() {
		parts = append(parts, "after:"+q.After.Format("2006/01/02"))
	}
	if !q.Before.IsZero() {
		parts = append(parts, "before:"+q.Before.Format("2006/01/02"))
	}
	if q.RawQuery != "" {
		parts = append(parts, q.RawQuery)
	}

	return strings.Join(parts, " ")
}

// quoteIfNeeded wraps a value in double quotes when it contains spaces
// or quotes Gmail's parser would otherwise split on. Single addresses
// don't need it; display names like "Alice Doe <alice@x.com>" do.
func quoteIfNeeded(s string) string {
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}

	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
