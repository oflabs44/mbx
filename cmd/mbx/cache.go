package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/cache"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/folder"
	"github.com/oflabs44/mbx/internal/output"
)

// aliasResolver adapts *config.Config (3-return Resolve) onto the
// 2-return cache.AliasResolver interface. The cache package can't
// depend on internal/config without an import cycle.
type aliasResolver struct{ cfg *config.Config }

func (r aliasResolver) Resolve(name string) (string, bool) {
	cname, _, ok := r.cfg.Resolve(name)
	return cname, ok
}

func resolverFor(cfg *config.Config) cache.AliasResolver { return aliasResolver{cfg: cfg} }

func newCacheCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the opt-in SQLite envelope cache (derived state)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newCacheSyncCmd(g, stdout, stderr),
		newCacheListCmd(g, stdout, stderr),
		newCacheSearchCmd(g, stdout, stderr),
		newCacheStatusCmd(g, stdout, stderr),
		newCacheClearCmd(g, stdout, stderr),
	)
	return cmd
}

// openCache opens the cache at the configured (or defaulted) path,
// translating common failure modes into stable codes.
func openCache(cfg *config.Config) (*cache.Store, error) {
	path, err := cache.DefaultPath(cfg.CacheDir)
	if err != nil {
		return nil, output.Errorf(output.CodeCacheUnavailable, "resolving cache path: %s", err.Error())
	}
	st, err := cache.Open(path)
	if err != nil {
		if errors.Is(err, cache.ErrSchemaMismatch) {
			return nil, output.Errorf(output.CodeCacheSchemaMismatch, "%s", err.Error()).
				WithDetails("path", path)
		}
		return nil, output.Errorf(output.CodeCacheUnavailable, "opening cache %s: %s", path, err.Error())
	}
	return st, nil
}

// cacheSyncFlags collects the per-invocation knobs `mbx cache sync` takes.
type cacheSyncFlags struct {
	folder string
	days   int
	all    bool
}

func newCacheSyncCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	cf := &cacheSyncFlags{}
	c := &cobra.Command{
		Use:   "sync",
		Short: "Pull envelopes from one or more accounts into the cache",
		Example: `  mbx cache sync -a work
  mbx cache sync -a work --folder INBOX --days 90
  mbx cache sync -a work,gmail-personal --all`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCacheSync(cmd.Context(), g, stdout, stderr, cf)
		},
	}
	f := c.Flags()
	f.StringVar(&cf.folder, "folder", "", "Limit to one folder. Default: account's configured cache folders or INBOX.")
	f.IntVar(&cf.days, "days", 0, "Days to sync back. Default: account's cache.sync_days or 30.")
	f.BoolVar(&cf.all, "all", false, "Sync every folder on the account.")
	return c
}

// cacheSyncResult is the per-account-folder JSON shape sync emits.
type cacheSyncResult struct {
	Account   string `json:"account"`
	Folder    string `json:"folder"`
	Envelopes int    `json:"envelopes"`
}

func runCacheSync(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, cf *cacheSyncFlags) error {
	if cf.all && cf.folder != "" {
		return output.Errorf(output.CodeUsageInvalid, "--all and --folder are mutually exclusive")
	}
	cfg, err := loadConfig(g)
	if err != nil {
		return err
	}
	canonical, accts, err := resolveAccountList(cfg, g.Accounts)
	if err != nil {
		return err
	}
	if len(canonical) == 0 {
		return output.Errorf(output.CodeInputMissingFlag, "missing required flag -a/--account")
	}

	st, err := openCache(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	results := make([]cacheSyncResult, 0, len(canonical))
	perAcctErrs := map[string]*output.Failure{}

	for _, cname := range canonical {
		acct := accts[cname]
		folders := pickSyncFolders(cf, acct)
		if cf.all {
			folders = nil // resolved per-backend below
		}
		days := pickSyncDays(cf, acct)
		after := time.Now().UTC().AddDate(0, 0, -days)

		b, berr := newBackend(ctx, cname, acct)
		if berr != nil {
			perAcctErrs[cname] = output.AsFailure(berr)
			if g.Strict {
				return berr
			}
			continue
		}

		if cf.all {
			lister, ok := b.(folder.Lister)
			if !ok {
				closeBackend(b)
				err := output.Errorf(output.CodeProviderUnsupported,
					"backend %q does not support folder listing required by --all", acct.Backend.Type)
				perAcctErrs[cname] = output.AsFailure(err)
				if g.Strict {
					return err
				}
				continue
			}
			fs, err := lister.ListFolders(ctx)
			if err != nil {
				closeBackend(b)
				perAcctErrs[cname] = output.AsFailure(err)
				if g.Strict {
					return err
				}
				continue
			}
			folders = make([]string, 0, len(fs))
			for _, f := range fs {
				folders = append(folders, f.Name)
			}
		}

		for _, folder := range folders {
			r, err := syncOneFolder(ctx, st, cname, b, folder, after, cfg)
			if err != nil {
				perAcctErrs[cname+"/"+folder] = output.AsFailure(err)
				if g.Strict {
					closeBackend(b)
					return err
				}
				continue
			}
			results = append(results, r)
		}
		closeBackend(b)
	}

	meta := envelopeListMeta{AccountsQueried: canonical}
	if len(perAcctErrs) > 0 {
		meta.Errors = perAcctErrs
	}
	return output.NewWriter(stdout, stderr, g.format()).Success(results, meta)
}

// pickSyncFolders resolves the folder list a sync should hit, in order
// of precedence: --folder, account's cache.folders, the account's
// inbox alias.
func pickSyncFolders(cf *cacheSyncFlags, acct *config.Account) []string {
	if cf.folder != "" {
		return []string{cf.folder}
	}
	if acct.Cache != nil && len(acct.Cache.Folders) > 0 {
		return acct.Cache.Folders
	}
	return []string{canonicalInbox(acct)}
}

// pickSyncDays resolves the days-back window for a sync. --days wins;
// then account cache.sync_days; default 30.
func pickSyncDays(cf *cacheSyncFlags, acct *config.Account) int {
	if cf.days > 0 {
		return cf.days
	}
	if acct.Cache != nil && acct.Cache.SyncDays > 0 {
		return acct.Cache.SyncDays
	}
	return 30
}

// syncOneFolder pages through the backend's envelope listing for a
// single folder, writing each page through to the cache. Returns the
// count and (for IMAP) the uidvalidity of the SELECT.
func syncOneFolder(ctx context.Context, st *cache.Store, cname string, b backend, folder string, after time.Time, cfg *config.Config) (cacheSyncResult, error) {
	resolver := resolverFor(cfg)
	total := 0
	cursor := ""
	for {
		page, err := envelope.List(ctx, b, envelope.ListQuery{
			Folder: folder,
			Limit:  envelope.MaxLimit,
			After:  after,
			Cursor: cursor,
		})
		if err != nil {
			return cacheSyncResult{}, err
		}
		if len(page.Envelopes) > 0 {
			if err := st.PutEnvelopes(ctx, cname, page.Envelopes, resolver); err != nil {
				return cacheSyncResult{}, fmt.Errorf("cache write: %w", err)
			}
			total += len(page.Envelopes)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	// uidvalidity surfacing is provider-specific and not exposed via the
	// narrow Lister interface; capturing it would require a separate
	// capability. Phase 6 leaves uidvalidity unset in the cache sync_state
	// row for the Gmail side and lets the IMAP probe fill it on the next
	// list. The sync_state row still gets stamped so `cache status`
	// reflects the run.
	if err := st.UpsertSyncState(ctx, cname, folder, 0, total, resolver); err != nil {
		return cacheSyncResult{}, fmt.Errorf("cache sync_state: %w", err)
	}
	return cacheSyncResult{Account: cname, Folder: folder, Envelopes: total}, nil
}

func newCacheListCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	ef := &envelopeFlags{}
	c := &cobra.Command{
		Use:   "list",
		Short: "List cached envelopes (no live API calls)",
		Example: `  mbx cache list -a work --unread
  mbx cache list -a work,gmail-personal --limit 50`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := ef.toListQuery(cmd)
			if err != nil {
				return err
			}
			return runCacheList(cmd.Context(), g, stdout, stderr, q, "")
		},
	}
	bindEnvelopeFlags(c, ef)
	return c
}

func newCacheSearchCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	ef := &envelopeFlags{}
	c := &cobra.Command{
		Use:     "search <keywords>",
		Short:   "Search cached envelopes (no live API calls)",
		Example: `  mbx cache search -a work "invoice"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := ef.toListQuery(cmd)
			if err != nil {
				return err
			}
			return runCacheList(cmd.Context(), g, stdout, stderr, q, args[0])
		},
	}
	bindEnvelopeFlags(c, ef)
	return c
}

func runCacheList(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, q envelope.ListQuery, keywords string) error {
	cfg, err := loadConfig(g)
	if err != nil {
		return err
	}
	canonical, _, err := resolveAccountList(cfg, g.Accounts)
	if err != nil {
		return err
	}
	if len(canonical) == 0 {
		return output.Errorf(output.CodeInputMissingFlag, "missing required flag -a/--account")
	}

	st, err := openCache(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	cq := cache.ListQuery{
		ListQuery: q,
		Accounts:  canonical,
		Keywords:  strings.TrimSpace(keywords),
	}
	page, err := st.ListEnvelopes(ctx, cq, resolverFor(cfg))
	if err != nil {
		return output.Errorf(output.CodeCacheUnavailable, "cache list: %s", err.Error())
	}
	envelopes := page.Envelopes
	if envelopes == nil {
		envelopes = []envelope.Envelope{}
	}
	meta := envelopeListMeta{AccountsQueried: canonical}
	if page.NextCursor != "" {
		meta.NextCursors = map[string]string{"_cache": page.NextCursor}
	}
	return output.NewWriter(stdout, stderr, g.format()).Success(envelopes, meta)
}

// cacheStatusResult is the JSON shape `mbx cache status` emits.
type cacheStatusResult struct {
	Path          string                `json:"path"`
	SizeBytes     int64                 `json:"size_bytes"`
	SchemaVersion string                `json:"schema_version"`
	Accounts      []cache.AccountStatus `json:"accounts"`
}

func newCacheStatusCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report cache row counts, last sync, and synced folders per account",
		Example: `  mbx cache status
  mbx cache status -a work`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCacheStatus(cmd.Context(), g, stdout, stderr)
		},
	}
}

func runCacheStatus(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer) error {
	cfg, err := loadConfig(g)
	if err != nil {
		return err
	}
	var accounts []string
	if len(g.Accounts) > 0 {
		c, _, err := resolveAccountList(cfg, g.Accounts)
		if err != nil {
			return err
		}
		accounts = c
	}

	path, err := cache.DefaultPath(cfg.CacheDir)
	if err != nil {
		return output.Errorf(output.CodeCacheUnavailable, "resolving cache path: %s", err.Error())
	}

	res := cacheStatusResult{Path: path, SchemaVersion: cache.SchemaVersion, Accounts: []cache.AccountStatus{}}
	if info, err := os.Stat(path); err == nil {
		res.SizeBytes = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return output.Errorf(output.CodeCacheUnavailable, "stat cache: %s", err.Error())
	} else {
		// File doesn't exist yet — return an empty result with the
		// resolved path so the user knows where it would be created.
		return output.NewWriter(stdout, stderr, g.format()).Success(res, nil)
	}

	st, err := openCache(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	statuses, err := st.Status(ctx, accounts, resolverFor(cfg))
	if err != nil {
		return output.Errorf(output.CodeCacheUnavailable, "cache status: %s", err.Error())
	}
	res.Accounts = statuses
	return output.NewWriter(stdout, stderr, g.format()).Success(res, nil)
}

type cacheClearResult struct {
	Accounts []string `json:"accounts"`
	Cleared  bool     `json:"cleared"`
}

func newCacheClearCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:     "clear",
		Short:   "Drop cache rows for the account(s); next `cache sync` rebuilds",
		Example: `  mbx cache clear -a work`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCacheClear(cmd.Context(), g, stdout, stderr)
		},
	}
}

func runCacheClear(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer) error {
	cfg, err := loadConfig(g)
	if err != nil {
		return err
	}
	canonical, _, err := resolveAccountList(cfg, g.Accounts)
	if err != nil {
		return err
	}
	if len(canonical) == 0 {
		return output.Errorf(output.CodeInputMissingFlag, "missing required flag -a/--account")
	}

	path, err := cache.DefaultPath(cfg.CacheDir)
	if err != nil {
		return output.Errorf(output.CodeCacheUnavailable, "resolving cache path: %s", err.Error())
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		// Nothing to clear; report idempotent success rather than failing.
		return output.NewWriter(stdout, stderr, g.format()).Success(
			cacheClearResult{Accounts: canonical, Cleared: false}, nil)
	}

	st, err := openCache(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	for _, cname := range canonical {
		if err := st.ClearAccount(ctx, cname, resolverFor(cfg)); err != nil {
			return output.Errorf(output.CodeCacheUnavailable, "cache clear %s: %s", cname, err.Error())
		}
	}
	return output.NewWriter(stdout, stderr, g.format()).Success(
		cacheClearResult{Accounts: canonical, Cleared: true}, nil)
}
