package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oflabs44/mbx/internal/envelope"
)

// PutEnvelopes upserts a batch of envelopes for one (account, folder).
// Caller passes the canonical account name; IDs are canonicalized at
// the boundary so alias-form rows can never accumulate (ADR-0008).
//
// Existing rows for these IDs have their envelope_flags and
// envelope_folders join entries replaced (a simple delete-then-insert
// inside the same transaction) because the source-of-truth state may
// have changed between syncs.
func (s *Store) PutEnvelopes(ctx context.Context, account string, envs []envelope.Envelope, resolver AliasResolver) error {
	if len(envs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	envIns, err := tx.PrepareContext(ctx, `
		INSERT INTO envelopes (id, account, thread_id, from_addr, to_addrs, cc_addrs, subject, date, snippet, has_attach, provider, gmail_labels, synced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			account=excluded.account,
			thread_id=excluded.thread_id,
			from_addr=excluded.from_addr,
			to_addrs=excluded.to_addrs,
			cc_addrs=excluded.cc_addrs,
			subject=excluded.subject,
			date=excluded.date,
			snippet=excluded.snippet,
			has_attach=excluded.has_attach,
			provider=excluded.provider,
			gmail_labels=excluded.gmail_labels,
			synced_at=excluded.synced_at
	`)
	if err != nil {
		return fmt.Errorf("prepare envelope insert: %w", err)
	}
	defer envIns.Close()

	flagDel, err := tx.PrepareContext(ctx, `DELETE FROM envelope_flags WHERE envelope_id=?`)
	if err != nil {
		return fmt.Errorf("prepare flag delete: %w", err)
	}
	defer flagDel.Close()

	flagIns, err := tx.PrepareContext(ctx, `INSERT INTO envelope_flags (envelope_id, flag) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare flag insert: %w", err)
	}
	defer flagIns.Close()

	folderDel, err := tx.PrepareContext(ctx, `DELETE FROM envelope_folders WHERE envelope_id=?`)
	if err != nil {
		return fmt.Errorf("prepare folder delete: %w", err)
	}
	defer folderDel.Close()

	folderIns, err := tx.PrepareContext(ctx, `INSERT INTO envelope_folders (envelope_id, folder) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare folder insert: %w", err)
	}
	defer folderIns.Close()

	cAccount := canonicalizeAccount(account, resolver)
	now := time.Now().UTC().Unix()
	for _, e := range envs {
		id := canonicalizeID(e.ID, resolver)
		threadID := canonicalizeID(e.ThreadID, resolver)
		gmailLabels := ""
		if e.Gmail != nil && len(e.Gmail.Labels) > 0 {
			b, err := json.Marshal(e.Gmail.Labels)
			if err != nil {
				return fmt.Errorf("marshal gmail labels: %w", err)
			}
			gmailLabels = string(b)
		}
		hasAttach := 0
		if e.HasAttachment {
			hasAttach = 1
		}
		date := e.Date.Unix()
		if e.Date.IsZero() {
			date = 0
		}
		if _, err := envIns.ExecContext(ctx,
			id, cAccount, nullable(threadID),
			nullable(e.From), commaJoin(e.To), commaJoin(e.Cc),
			nullable(e.Subject), date, nullable(e.Snippet),
			hasAttach, e.Provider, nullable(gmailLabels), now,
		); err != nil {
			return fmt.Errorf("insert envelope %s: %w", id, err)
		}

		if _, err := flagDel.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("clear flags %s: %w", id, err)
		}
		for _, f := range e.Flags {
			if _, err := flagIns.ExecContext(ctx, id, f); err != nil {
				return fmt.Errorf("insert flag %s/%s: %w", id, f, err)
			}
		}

		if _, err := folderDel.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("clear folders %s: %w", id, err)
		}
		for _, fold := range e.Folders {
			if _, err := folderIns.ExecContext(ctx, id, fold); err != nil {
				return fmt.Errorf("insert folder %s/%s: %w", id, fold, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit envelope upsert: %w", err)
	}
	return nil
}

// ListEnvelopes queries the cache. Accounts is the canonical-name list
// the caller already resolved; resolver canonicalizes filter inputs
// (e.g. an alias-form account passed via -a).
//
// Cursor is a unix-second timestamp keyset: next page's WHERE adds
// date < cursor, so callers can page through `WHERE account IN (...)
// ORDER BY date DESC` deterministically without offset drift.
func (s *Store) ListEnvelopes(ctx context.Context, q ListQuery, resolver AliasResolver) (envelope.Page, error) {
	accounts := canonicalizeAccounts(q.Accounts, resolver)
	if len(accounts) == 0 {
		return envelope.Page{Envelopes: []envelope.Envelope{}}, nil
	}

	var where []string
	var args []any

	placeholders := make([]string, len(accounts))
	for i, a := range accounts {
		placeholders[i] = "?"
		args = append(args, a)
	}
	where = append(where, "e.account IN ("+strings.Join(placeholders, ",")+")")

	if q.Folder != "" {
		where = append(where, "EXISTS (SELECT 1 FROM envelope_folders f WHERE f.envelope_id=e.id AND f.folder=?)")
		args = append(args, q.Folder)
	}
	if !q.After.IsZero() {
		where = append(where, "e.date >= ?")
		args = append(args, q.After.Unix())
	}
	if !q.Before.IsZero() {
		where = append(where, "e.date <= ?")
		args = append(args, q.Before.Unix())
	}
	if q.From != "" {
		where = append(where, "e.from_addr LIKE ?")
		args = append(args, "%"+q.From+"%")
	}
	if q.To != "" {
		where = append(where, "e.to_addrs LIKE ?")
		args = append(args, "%"+q.To+"%")
	}
	if q.Unread != nil {
		if *q.Unread {
			where = append(where, "NOT EXISTS (SELECT 1 FROM envelope_flags f WHERE f.envelope_id=e.id AND f.flag='seen')")
		} else {
			where = append(where, "EXISTS (SELECT 1 FROM envelope_flags f WHERE f.envelope_id=e.id AND f.flag='seen')")
		}
	}
	if q.Starred != nil && *q.Starred {
		where = append(where, "EXISTS (SELECT 1 FROM envelope_flags f WHERE f.envelope_id=e.id AND f.flag='flagged')")
	}
	if q.HasAttachment != nil && *q.HasAttachment {
		where = append(where, "e.has_attach=1")
	}
	if q.Keywords != "" {
		where = append(where, "(e.subject LIKE ? OR e.snippet LIKE ? OR e.from_addr LIKE ?)")
		kw := "%" + q.Keywords + "%"
		args = append(args, kw, kw, kw)
	}
	if cur := parseCursor(q.Cursor); cur > 0 {
		where = append(where, "e.date < ?")
		args = append(args, cur)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = envelope.DefaultLimit
	}
	if limit > envelope.MaxLimit {
		limit = envelope.MaxLimit
	}

	sqlStr := `SELECT e.id, e.account, e.thread_id, e.from_addr, e.to_addrs, e.cc_addrs, e.subject, e.date, e.snippet, e.has_attach, e.provider, e.gmail_labels
		FROM envelopes e
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY e.date DESC
		LIMIT ?`
	args = append(args, limit+1) // peek one ahead to know if there's another page

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return envelope.Page{}, fmt.Errorf("query envelopes: %w", err)
	}
	defer rows.Close()

	envs := make([]envelope.Envelope, 0, limit)
	ids := make([]string, 0, limit)
	for rows.Next() {
		var (
			id, account                                            string
			threadID, fromAddr, toAddrs, ccAddrs, subject, snippet sql.NullString
			gmailLabels                                            sql.NullString
			date                                                   int64
			hasAttach                                              int
			provider                                               string
		)
		if err := rows.Scan(&id, &account, &threadID, &fromAddr, &toAddrs, &ccAddrs, &subject, &date, &snippet, &hasAttach, &provider, &gmailLabels); err != nil {
			return envelope.Page{}, fmt.Errorf("scan envelope: %w", err)
		}
		if len(envs) == limit {
			// peeked past page; don't include but use date to mark next cursor
			break
		}
		e := envelope.Envelope{
			ID:            id,
			Account:       account,
			ThreadID:      threadID.String,
			From:          fromAddr.String,
			To:            commaSplit(toAddrs.String),
			Cc:            commaSplit(ccAddrs.String),
			Subject:       subject.String,
			Snippet:       snippet.String,
			HasAttachment: hasAttach != 0,
			Provider:      provider,
			Flags:         []string{},
			Folders:       []string{},
		}
		if date > 0 {
			e.Date = time.Unix(date, 0).UTC()
		}
		if gmailLabels.Valid && gmailLabels.String != "" {
			var labels []string
			if err := json.Unmarshal([]byte(gmailLabels.String), &labels); err == nil {
				e.Gmail = &envelope.GmailExtras{Labels: labels}
			}
		}
		envs = append(envs, e)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return envelope.Page{}, fmt.Errorf("iter envelopes: %w", err)
	}

	if err := s.attachFlagsAndFolders(ctx, envs, ids); err != nil {
		return envelope.Page{}, err
	}

	page := envelope.Page{Envelopes: envs}
	if len(envs) == limit {
		// We hit the limit; check if there was a peeked-past row.
		// rows.Next was already called; if it returned true once more,
		// we broke before appending. Use the last included envelope's
		// date as the next cursor.
		last := envs[len(envs)-1]
		if !last.Date.IsZero() {
			page.NextCursor = strconv.FormatInt(last.Date.Unix(), 10)
		}
	}
	return page, nil
}

func (s *Store) attachFlagsAndFolders(ctx context.Context, envs []envelope.Envelope, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]int, len(envs))
	for i, e := range envs {
		byID[e.ID] = i
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	inClause := "(" + strings.Join(ph, ",") + ")"

	flagRows, err := s.db.QueryContext(ctx, `SELECT envelope_id, flag FROM envelope_flags WHERE envelope_id IN `+inClause, args...)
	if err != nil {
		return fmt.Errorf("query flags: %w", err)
	}
	for flagRows.Next() {
		var id, flag string
		if err := flagRows.Scan(&id, &flag); err != nil {
			flagRows.Close()
			return fmt.Errorf("scan flag: %w", err)
		}
		if i, ok := byID[id]; ok {
			envs[i].Flags = append(envs[i].Flags, flag)
		}
	}
	if err := flagRows.Err(); err != nil {
		flagRows.Close()
		return fmt.Errorf("iter flags: %w", err)
	}
	flagRows.Close()

	folderRows, err := s.db.QueryContext(ctx, `SELECT envelope_id, folder FROM envelope_folders WHERE envelope_id IN `+inClause, args...)
	if err != nil {
		return fmt.Errorf("query folders: %w", err)
	}
	for folderRows.Next() {
		var id, folder string
		if err := folderRows.Scan(&id, &folder); err != nil {
			folderRows.Close()
			return fmt.Errorf("scan folder: %w", err)
		}
		if i, ok := byID[id]; ok {
			envs[i].Folders = append(envs[i].Folders, folder)
		}
	}
	if err := folderRows.Err(); err != nil {
		folderRows.Close()
		return fmt.Errorf("iter folders: %w", err)
	}
	folderRows.Close()
	return nil
}

// UpdateFlags applies a flag delta to the cache row for a single ID.
// Best-effort: any error is the caller's to log, never to fail the
// surrounding live verb (ADR-0003).
func (s *Store) UpdateFlags(ctx context.Context, idStr string, add, remove []envelope.Flag, resolver AliasResolver) error {
	id := canonicalizeID(idStr, resolver)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, f := range remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM envelope_flags WHERE envelope_id=? AND flag=?`, id, string(f)); err != nil {
			return fmt.Errorf("remove flag %s: %w", f, err)
		}
	}
	for _, f := range add {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO envelope_flags (envelope_id, flag) VALUES (?, ?)`, id, string(f)); err != nil {
			return fmt.Errorf("add flag %s: %w", f, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit flag update: %w", err)
	}
	return nil
}

// DeleteByIDs removes envelope rows (and their join entries via cascade).
// Used by move/delete write-through — the cache representation of the
// envelope is no longer correct after the live verb's mutation; rather
// than chase the new state across providers, we drop the row and let the
// next sync repopulate.
func (s *Store) DeleteByIDs(ctx context.Context, ids []string, resolver AliasResolver) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, raw := range ids {
		id := canonicalizeID(raw, resolver)
		if _, err := tx.ExecContext(ctx, `DELETE FROM envelopes WHERE id=?`, id); err != nil {
			return fmt.Errorf("delete envelope %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete: %w", err)
	}
	return nil
}

// ClearAccount removes every envelope (and cascaded rows) for an account,
// plus its sync_state entries. Used by `mbx cache clear`.
func (s *Store) ClearAccount(ctx context.Context, account string, resolver AliasResolver) error {
	cname := canonicalizeAccount(account, resolver)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM envelopes WHERE account=?`, cname); err != nil {
		return fmt.Errorf("delete envelopes for %s: %w", cname, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_state WHERE account=?`, cname); err != nil {
		return fmt.Errorf("delete sync_state for %s: %w", cname, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clear: %w", err)
	}
	return nil
}

// ListQuery is the cache-side projection of envelope.ListQuery plus
// the account list (cache reads are inherently multi-account; live
// verbs fanout via cmd/mbx/fanout.go) and a Keywords field that folds
// `cache search` back into `cache list` at the SQL layer.
//
// envelope.ListQuery.RawQuery is not honored by the cache: provider-
// native search syntax has no equivalent over the LIKE-based SQL we
// run; use Keywords or the structured filters instead.
type ListQuery struct {
	envelope.ListQuery
	Accounts []string
	Keywords string
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func commaJoin(ss []string) any {
	if len(ss) == 0 {
		return nil
	}
	return strings.Join(ss, ", ")
}

func commaSplit(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseCursor(s string) int64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
