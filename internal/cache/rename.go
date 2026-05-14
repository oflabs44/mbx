package cache

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/oflabs44/mbx/internal/mbxid"
)

// RenameAccount migrates every row keyed under oldName to newName:
// envelope IDs are re-stamped in place (the account segment changes;
// the rest is unchanged), envelope.account is updated, sync_state.account
// is updated. Called by the account rename verb after the TOML rewrite
// succeeds (ADR-0008).
//
// Best-effort from the verb's perspective: a failure here doesn't undo
// the rename — the user can `cache clear && cache sync` to recover.
func (s *Store) RenameAccount(ctx context.Context, oldName, newName string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := rekeyEnvelopeIDs(ctx, tx, oldName, newName); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE envelopes  SET account=? WHERE account=?`, newName, oldName); err != nil {
		return fmt.Errorf("update envelopes.account: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sync_state SET account=? WHERE account=?`, newName, oldName); err != nil {
		return fmt.Errorf("update sync_state.account: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rename: %w", err)
	}
	return nil
}

// rekeyEnvelopeIDs walks every envelope row owned by oldName, parses
// its mbx ID, re-stamps the Account segment, and updates the row's
// primary key. The join tables (envelope_flags, envelope_folders,
// messages) reference envelopes(id) with ON UPDATE CASCADE so the
// child rows follow automatically.
func rekeyEnvelopeIDs(ctx context.Context, tx *sql.Tx, oldName, newName string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM envelopes WHERE account=?`, oldName)
	if err != nil {
		return fmt.Errorf("select old envelope ids: %w", err)
	}
	defer rows.Close()

	var oldIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan old id: %w", err)
		}
		oldIDs = append(oldIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, oldID := range oldIDs {
		parsed, err := mbxid.Parse(oldID)
		if err != nil {
			// Row exists but ID is malformed; leave it and let cache
			// clear sweep it later. Renaming around it is safer than
			// dropping the row mid-rename.
			continue
		}
		parsed.Account = newName
		newID := parsed.String()
		if newID == oldID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE envelopes SET id=? WHERE id=?`, newID, oldID); err != nil {
			return fmt.Errorf("update envelopes id: %w", err)
		}
	}
	return nil
}

// NewStaticResolver returns an AliasResolver covering a single
// alias→canonical mapping. Useful for the post-rename canonicalize
// hook where the caller doesn't have a Config in hand.
func NewStaticResolver(alias, canonical string) AliasResolver {
	return staticResolver{alias: alias, canonical: canonical}
}
