package cache

import (
	"strings"

	"github.com/oflabs44/mbx/internal/mbxid"
)

// AliasResolver maps a name (canonical or alias) to the canonical
// account name. The two methods cover the two callers — `cache list`
// fanout passes a Config; the rename verb constructs a one-off mapping.
type AliasResolver interface {
	Resolve(name string) (canonical string, ok bool)
}

// canonicalizeID re-stamps id's Account segment with the canonical name
// per the alias resolver. Returns the original string unchanged when the
// account is already canonical or unknown — Store callers should reject
// unknown accounts upstream; the unknown-passthrough here is just to
// preserve any pre-cached rows whose canonical name has since been
// renamed and forgotten.
func canonicalizeID(idStr string, r AliasResolver) string {
	if r == nil {
		return idStr
	}
	id, err := mbxid.Parse(idStr)
	if err != nil {
		return idStr
	}
	cname, ok := r.Resolve(id.Account)
	if !ok || cname == id.Account {
		return idStr
	}
	id.Account = cname
	return id.String()
}

// canonicalizeAccount maps a single name through the resolver. Empty
// input or unknown name returns the input unchanged.
func canonicalizeAccount(name string, r AliasResolver) string {
	if r == nil || name == "" {
		return name
	}
	if cname, ok := r.Resolve(name); ok {
		return cname
	}
	return name
}

// canonicalizeAccounts applies canonicalizeAccount to a slice, dedup'ing
// duplicates that arise when both an alias and its canonical were passed.
// Preserves input order of the first occurrence.
func canonicalizeAccounts(names []string, r AliasResolver) []string {
	if len(names) == 0 {
		return names
	}
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		c := canonicalizeAccount(n, r)
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

// staticResolver is a one-shot AliasResolver built from a single
// (alias → canonical) pair. Used by the rename verb so the post-rename
// canonicalize hook doesn't need a full Config.
type staticResolver struct {
	alias     string
	canonical string
}

func (s staticResolver) Resolve(name string) (string, bool) {
	if strings.EqualFold(name, s.alias) {
		return s.canonical, true
	}
	if name == s.canonical {
		return s.canonical, true
	}
	return "", false
}
