package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/oflabs44/mbx/internal/cache"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/mbxid"
)

// writeThroughFlags applies a flag delta to every cached envelope row
// matching ids. Best-effort: ADR-0003 — never blocks command exit; the
// cache stays as derived state. Failures log to stderr when --verbose is
// set; otherwise are silently dropped (the live verb already succeeded).
func writeThroughFlags(ctx context.Context, g *GlobalFlags, stderr io.Writer, cfg *config.Config, ids []mbxid.ID, add, remove []envelope.Flag) {
	st, ok := tryOpenCacheForWriteThrough(g, stderr, cfg)
	if !ok {
		return
	}
	defer st.Close()

	resolver := resolverFor(cfg)
	for _, id := range ids {
		if err := st.UpdateFlags(ctx, id.String(), add, remove, resolver); err != nil {
			logWriteThroughErr(g, stderr, "envelope flag", err)
		}
	}
}

// writeThroughDelete removes envelope rows for ids from the cache.
// Used by both message move and message delete — after either, the
// cache's view of the envelope is stale; the next `cache sync` will
// repopulate from the new authoritative state if appropriate.
func writeThroughDelete(ctx context.Context, g *GlobalFlags, stderr io.Writer, cfg *config.Config, ids []mbxid.ID) {
	st, ok := tryOpenCacheForWriteThrough(g, stderr, cfg)
	if !ok {
		return
	}
	defer st.Close()

	idStrs := make([]string, len(ids))
	for i, id := range ids {
		idStrs[i] = id.String()
	}
	if err := st.DeleteByIDs(ctx, idStrs, resolverFor(cfg)); err != nil {
		logWriteThroughErr(g, stderr, "cache delete", err)
	}
}

// tryOpenCacheForWriteThrough opens the cache only if the file exists —
// no automatic creation from a mutating live verb. A missing file
// signals the user hasn't opted in to caching; we don't materialize one
// behind their back. Schema mismatches and other errors are logged at
// --verbose and skipped (ADR-0003).
func tryOpenCacheForWriteThrough(g *GlobalFlags, stderr io.Writer, cfg *config.Config) (*cache.Store, bool) {
	path, err := cache.DefaultPath(cfg.CacheDir)
	if err != nil {
		logWriteThroughErr(g, stderr, "cache path", err)
		return nil, false
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, false
	} else if err != nil {
		logWriteThroughErr(g, stderr, "cache stat", err)
		return nil, false
	}
	st, err := cache.Open(path)
	if err != nil {
		logWriteThroughErr(g, stderr, "cache open", err)
		return nil, false
	}
	return st, true
}

// cacheInvalidateAfterMutation is the call-site helper for message
// move / message delete: load config (silently skipping write-through
// on a load failure — the live verb already won), canonicalize the IDs,
// and drop the corresponding cache rows.
func cacheInvalidateAfterMutation(ctx context.Context, g *GlobalFlags, stderr io.Writer, ids []mbxid.ID, cname string) {
	cfg, err := loadConfig(g)
	if err != nil {
		logWriteThroughErr(g, stderr, "load config", err)
		return
	}
	writeThroughDelete(ctx, g, stderr, cfg, canonicalizeIDsForAccount(ids, cname))
}

// cacheApplyFlagsAfterMutation is the call-site helper for envelope
// flag write-through: load config silently (live verb already won),
// canonicalize the IDs, and apply the flag delta to the cache.
func cacheApplyFlagsAfterMutation(ctx context.Context, g *GlobalFlags, stderr io.Writer, ids []mbxid.ID, cname string, add, remove []envelope.Flag) {
	cfg, err := loadConfig(g)
	if err != nil {
		logWriteThroughErr(g, stderr, "load config", err)
		return
	}
	writeThroughFlags(ctx, g, stderr, cfg, canonicalizeIDsForAccount(ids, cname), add, remove)
}

// canonicalizeIDsForAccount returns a copy of ids with each ID's Account
// segment stamped to cname (the resolved canonical name). The cache layer
// canonicalizes again via its AliasResolver, but stamping here keeps the
// write-through helpers' input shape uniform.
func canonicalizeIDsForAccount(ids []mbxid.ID, cname string) []mbxid.ID {
	out := make([]mbxid.ID, len(ids))
	for i, id := range ids {
		id.Account = cname
		out[i] = id
	}
	return out
}

// renameCacheRows updates the cache to reflect an account rename. Like
// other write-through, this is best-effort — the TOML rewrite already
// succeeded; a cache failure logs at --verbose and the user recovers
// via `cache clear && cache sync`.
func renameCacheRows(ctx context.Context, g *GlobalFlags, stderr io.Writer, oldName, newName string) {
	cfg, err := loadConfig(g)
	if err != nil {
		logWriteThroughErr(g, stderr, "load config (post-rename)", err)
		return
	}
	st, ok := tryOpenCacheForWriteThrough(g, stderr, cfg)
	if !ok {
		return
	}
	defer st.Close()
	if err := st.RenameAccount(ctx, oldName, newName); err != nil {
		logWriteThroughErr(g, stderr, "cache rename", err)
	}
}

func logWriteThroughErr(g *GlobalFlags, stderr io.Writer, what string, err error) {
	if !g.Verbose && !g.Debug {
		return
	}
	fmt.Fprintln(stderr, "cache write-through:", what+":", err.Error())
}
