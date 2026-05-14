package envelope

import (
	"context"
	"strings"
)

// SearchQuery is a list query plus a positional keyword string. The
// keyword string folds into RawQuery so backend query builders only have
// one place to assemble the wire form.
type SearchQuery struct {
	ListQuery
	Keywords string
}

// Search forwards to List after merging Keywords into RawQuery. Search is
// cross-folder by default (callers leave Folder empty); list defaults to
// the backend's home folder. The single difference between the two verbs
// lives at the handler layer, not here.
func Search(ctx context.Context, b Lister, q SearchQuery) (Page, error) {
	lq := q.ListQuery
	if kw := strings.TrimSpace(q.Keywords); kw != "" {
		if lq.RawQuery != "" {
			lq.RawQuery = lq.RawQuery + " " + kw
		} else {
			lq.RawQuery = kw
		}
	}
	return List(ctx, b, lq)
}
