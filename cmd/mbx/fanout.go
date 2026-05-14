package main

import (
	"context"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/output"
)

// fanoutResult carries what an envelope-fanout caller emits: merged
// envelopes (date desc) across successful accounts, plus the per-account
// meta fields the JSON response needs. AccountsQueried lists the canonical
// names dispatched to (in user-supplied order, dedup'd); NextCursors and
// Errors are keyed by the same names.
type fanoutResult struct {
	Envelopes       []envelope.Envelope
	AccountsQueried []string
	NextCursors     map[string]string
	Errors          map[string]*output.Failure
}

// envelopeWork is the per-account closure runEnvelopeFanout invokes
// against each account's already-constructed backend. The closure
// receives the canonical account name (for folder defaulting that
// depends on the account's config) and a context that fires on Strict
// failures so peers can cancel cleanly.
type envelopeWork func(ctx context.Context, cname string, acct *config.Account, b backend) (envelope.Page, error)

// runEnvelopeFanout dispatches an envelope-shaped read (list, search) to
// every account in -a in parallel. Default is partial success: any
// account that fails surfaces under .Errors, surviving accounts populate
// .Envelopes. With g.Strict, the first failure cancels the rest and the
// helper returns that error directly (no fanoutResult).
//
// If *every* account fails (partial-success mode), returns
// CodeFanoutAllFailed rather than an empty success — emitting a zero-data
// success envelope with all-errors-in-meta would be a silent failure for
// callers that branch on exit code or stdout content.
//
// Canonical and alias names both resolve via config.Resolve; the same
// account passed twice (canonical + alias) is dedup'd to one canonical
// entry. Wildcards in -a are rejected up front. The cursor parameter,
// when non-empty with multiple accounts, is rejected as ambiguous —
// callers should page accounts individually.
func runEnvelopeFanout(
	ctx context.Context,
	g *GlobalFlags,
	cursor string,
	work envelopeWork,
) (fanoutResult, error) {
	if len(g.Accounts) == 0 {
		return fanoutResult{}, output.Errorf(output.CodeInputMissingFlag, "missing required flag -a/--account")
	}
	for _, name := range g.Accounts {
		if strings.ContainsAny(name, "*?") {
			return fanoutResult{}, output.Errorf(output.CodeUsageInvalid,
				"wildcards are not allowed in -a (got %q)", name)
		}
	}
	cfg, err := loadConfig(g)
	if err != nil {
		return fanoutResult{}, err
	}

	canonical, accts, err := resolveAccountList(cfg, g.Accounts)
	if err != nil {
		return fanoutResult{}, err
	}
	if cursor != "" && len(canonical) > 1 {
		return fanoutResult{}, output.Errorf(output.CodeUsageInvalid,
			"--cursor is ambiguous with multi-account -a (got %d accounts); page accounts individually", len(canonical))
	}

	var mu sync.Mutex
	pages := make(map[string]envelope.Page, len(canonical))
	errs := map[string]*output.Failure{}

	grp, gctx := errgroup.WithContext(ctx)
	for _, cn := range canonical {
		acct := accts[cn]
		grp.Go(func() error {
			b, berr := newBackend(gctx, cn, acct)
			if berr != nil {
				return collectFanoutErr(g, &mu, errs, cn, berr)
			}
			defer closeBackend(b)
			page, werr := work(gctx, cn, acct, b)
			if werr != nil {
				return collectFanoutErr(g, &mu, errs, cn, werr)
			}
			mu.Lock()
			pages[cn] = page
			mu.Unlock()
			return nil
		})
	}
	if err := grp.Wait(); err != nil {
		return fanoutResult{}, err
	}

	if len(errs) == len(canonical) {
		// Every account failed; partial-success has nothing to be partial about.
		return fanoutResult{}, fanoutAllFailedError(canonical, errs)
	}

	var merged []envelope.Envelope
	var nextCursors map[string]string
	for _, cn := range canonical {
		page, ok := pages[cn]
		if !ok {
			continue
		}
		merged = append(merged, page.Envelopes...)
		if page.NextCursor != "" {
			if nextCursors == nil {
				nextCursors = map[string]string{}
			}
			nextCursors[cn] = page.NextCursor
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Date.After(merged[j].Date)
	})

	if len(errs) == 0 {
		errs = nil
	}
	return fanoutResult{
		Envelopes:       merged,
		AccountsQueried: canonical,
		NextCursors:     nextCursors,
		Errors:          errs,
	}, nil
}

// fanoutAllFailedError builds the structured CodeFanoutAllFailed error
// that surfaces when no account survived the fanout. The per-account
// failure map is attached under details.errors so callers can branch on
// individual codes without re-running.
func fanoutAllFailedError(accounts []string, errs map[string]*output.Failure) *output.Failure {
	return output.Errorf(output.CodeFanoutAllFailed,
		"all %d accounts in fanout failed; see details.errors per-account", len(accounts)).
		WithDetails("accounts", accounts).
		WithDetails("errors", errs)
}

// resolveAccountList walks the user-supplied -a list once, resolving each
// entry through config.Resolve and dedup'ing collisions (canonical +
// alias of the same account). Returns canonical names in input order and
// a name→Account map. Unknown names abort the whole command — partial
// success applies to runtime failures, not config typos.
func resolveAccountList(cfg *config.Config, names []string) ([]string, map[string]*config.Account, error) {
	canonical := make([]string, 0, len(names))
	accts := make(map[string]*config.Account, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		cname, acct, ok := cfg.Resolve(name)
		if !ok {
			return nil, nil, output.Errorf(output.CodeConfigUnknownAccount,
				"unknown account: %s", name).WithDetails("account", name)
		}
		if seen[cname] {
			continue
		}
		seen[cname] = true
		canonical = append(canonical, cname)
		accts[cname] = acct
	}
	return canonical, accts, nil
}

// collectFanoutErr records a per-account failure. In Strict mode it
// returns the error so errgroup cancels the context and cascades to
// peers; in partial-success mode it returns nil so peers keep running.
func collectFanoutErr(g *GlobalFlags, mu *sync.Mutex, errs map[string]*output.Failure, cname string, err error) error {
	mu.Lock()
	errs[cname] = output.AsFailure(err)
	mu.Unlock()
	if g.Strict {
		return err
	}
	return nil
}
