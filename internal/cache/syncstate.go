package cache

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// UpsertSyncState records that a sync of (account, folder) happened.
// envelopeN is the number of envelopes the sync wrote; uidValidity is
// non-zero only for IMAP. Idempotent — re-running over the same key
// updates the timestamp and count.
func (s *Store) UpsertSyncState(ctx context.Context, account, folder string, uidValidity uint32, envelopeN int, resolver AliasResolver) error {
	cname := canonicalizeAccount(account, resolver)
	var uvArg any = nil
	if uidValidity > 0 {
		uvArg = int64(uidValidity)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_state (account, folder, uidvalidity, last_sync_at, envelope_n)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(account, folder) DO UPDATE SET
			uidvalidity=excluded.uidvalidity,
			last_sync_at=excluded.last_sync_at,
			envelope_n=excluded.envelope_n
	`, cname, folder, uvArg, time.Now().UTC().Unix(), envelopeN)
	if err != nil {
		return fmt.Errorf("upsert sync_state %s/%s: %w", cname, folder, err)
	}
	return nil
}

// AccountStatus is the per-account aggregate `mbx cache status` returns.
// LastSyncAt is the max(last_sync_at) across this account's sync_state
// rows; zero when nothing has been synced.
type AccountStatus struct {
	Account    string    `json:"account"`
	Rows       int64     `json:"rows"`
	LastSyncAt time.Time `json:"last_sync_at,omitzero"`
	Folders    []string  `json:"folders"`
}

// Status computes per-account row counts, the latest sync_state row
// timestamp, and the synced folder list. Accounts is the canonical name
// list to report on; empty means every account known to the cache.
func (s *Store) Status(ctx context.Context, accounts []string, resolver AliasResolver) ([]AccountStatus, error) {
	cnames := canonicalizeAccounts(accounts, resolver)

	rowsByAcc, err := s.countRowsByAccount(ctx, cnames)
	if err != nil {
		return nil, err
	}
	foldersByAcc, lastByAcc, err := s.syncStateByAccount(ctx, cnames)
	if err != nil {
		return nil, err
	}

	known := map[string]struct{}{}
	for a := range rowsByAcc {
		known[a] = struct{}{}
	}
	for a := range foldersByAcc {
		known[a] = struct{}{}
	}
	for _, a := range cnames {
		known[a] = struct{}{}
	}

	out := make([]AccountStatus, 0, len(known))
	for a := range known {
		st := AccountStatus{
			Account:    a,
			Rows:       rowsByAcc[a],
			Folders:    foldersByAcc[a],
			LastSyncAt: lastByAcc[a],
		}
		if st.Folders == nil {
			st.Folders = []string{}
		}
		out = append(out, st)
	}

	if len(cnames) > 0 {
		order := map[string]int{}
		for i, a := range cnames {
			order[a] = i
		}
		sort.SliceStable(out, func(i, j int) bool { return order[out[i].Account] < order[out[j].Account] })
	} else {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	}
	return out, nil
}

func (s *Store) countRowsByAccount(ctx context.Context, accounts []string) (map[string]int64, error) {
	out := map[string]int64{}
	q, args := buildWhereIn("SELECT account, COUNT(*) FROM envelopes", "account", accounts, " GROUP BY account")
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("count rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		var n int64
		if err := rows.Scan(&a, &n); err != nil {
			return nil, err
		}
		out[a] = n
	}
	return out, rows.Err()
}

func (s *Store) syncStateByAccount(ctx context.Context, accounts []string) (map[string][]string, map[string]time.Time, error) {
	q, args := buildWhereIn("SELECT account, folder, last_sync_at FROM sync_state", "account", accounts, " ORDER BY account, folder")
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("read sync_state: %w", err)
	}
	defer rows.Close()
	folders := map[string][]string{}
	lasts := map[string]time.Time{}
	for rows.Next() {
		var account, folder string
		var last int64
		if err := rows.Scan(&account, &folder, &last); err != nil {
			return nil, nil, err
		}
		folders[account] = append(folders[account], folder)
		t := time.Unix(last, 0).UTC()
		if t.After(lasts[account]) {
			lasts[account] = t
		}
	}
	return folders, lasts, rows.Err()
}

// buildWhereIn produces `<prefix> WHERE col IN (?,?,...) <suffix>` when
// names is non-empty; otherwise `<prefix><suffix>` so the caller can
// reuse the same code path for the "all accounts" case.
func buildWhereIn(prefix, col string, names []string, suffix string) (string, []any) {
	if len(names) == 0 {
		return prefix + suffix, nil
	}
	ph := make([]string, len(names))
	args := make([]any, len(names))
	for i, n := range names {
		ph[i] = "?"
		args[i] = n
	}
	return prefix + " WHERE " + col + " IN (" + strings.Join(ph, ",") + ")" + suffix, args
}
