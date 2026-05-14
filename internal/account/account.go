// Package account is the verb layer for account-scoped operations: listing
// what's configured, looking one up, scaffolding new entries, and (in later
// Phase 1 tasks) running the OAuth lifecycle. The data model lives in
// internal/config; this package builds on it.
package account

import (
	"fmt"
	"sort"

	"github.com/oflabs44/mbx/internal/config"
)

// Info is the JSON shape returned by `mbx account list`. Field names are
// part of the documented output contract — see docs/commands.md.
type Info struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Email string `json:"email"`
	Cache bool   `json:"cache"`
}

// List returns one Info per configured account, sorted by name for
// deterministic output.
func List(c *config.Config) []Info {
	out := make([]Info, 0, len(c.Accounts))
	for name, a := range c.Accounts {
		out = append(out, Info{
			Name:  name,
			Type:  string(a.Type),
			Email: a.Email,
			Cache: a.Cache != nil,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns the named account or wraps config.ErrUnknownAccount.
func Lookup(c *config.Config, name string) (*config.Account, error) {
	a, ok := c.Account(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", config.ErrUnknownAccount, name)
	}
	return a, nil
}
