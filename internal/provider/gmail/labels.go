package gmail

// Single source of truth for how Gmail labels project into mbx output.
// Every codepath that touches labels (envelope listing, folder listing,
// flag mapping) reads from these tables — don't fork them per file.
//
// The mbx Flag vocabulary (CONTEXT.md) is the positive form
// (seen, flagged, answered, draft, deleted). UNREAD in Gmail is the
// inverse of mbx's "seen" — handled specially in flagsFromLabels.

// labelToFlag maps Gmail system labels to the positive-form mbx Flag
// vocabulary. UNREAD is intentionally NOT here; it inverts to seen-
// presence and is handled by flagsFromLabels directly.
var labelToFlag = map[string]string{
	"STARRED": "flagged",
	"DRAFT":   "draft",
}

// labelsHiddenFromFolders are labels that don't make sense as Folders.
// Pure-state labels (UNREAD, STARRED, IMPORTANT) and Gmail-internal
// labels (CHAT, the category tabs).
var labelsHiddenFromFolders = map[string]bool{
	"UNREAD":              true,
	"STARRED":             true,
	"IMPORTANT":           true,
	"CHAT":                true,
	"CATEGORY_PROMOTIONS": true,
	"CATEGORY_UPDATES":    true,
	"CATEGORY_SOCIAL":     true,
	"CATEGORY_FORUMS":     true,
	"CATEGORY_PERSONAL":   true,
}

// flagsFromLabels projects a Gmail label set onto the mbx Flag
// vocabulary. UNREAD's absence is what produces "seen" — Gmail uses
// negative-form (UNREAD) where mbx uses positive (seen).
func flagsFromLabels(labels []string) []string {
	out := []string{}
	hasUnread := false
	for _, l := range labels {
		if l == "UNREAD" {
			hasUnread = true
			continue
		}
		if f, ok := labelToFlag[l]; ok {
			out = append(out, f)
		}
	}
	if !hasUnread {
		out = append(out, "seen")
	}
	return out
}

func foldersFromLabels(labels []string) []string {
	out := []string{}
	for _, l := range labels {
		if labelsHiddenFromFolders[l] {
			continue
		}
		out = append(out, l)
	}
	return out
}
