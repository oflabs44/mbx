package imap

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"

	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/output"
)

// MoveMessages satisfies message.Mover.
//
// IDs are grouped by source folder; one SELECT+MOVE pair per group. The
// imapclient library transparently falls back to COPY+STORE+EXPUNGE on
// servers without the MOVE extension; mbx callers see the same return
// shape either way.
//
// New IDs come from the server's COPYUID response (requires UIDPLUS or
// IMAP4rev2). On servers without that, dest UIDs are not knowable and
// the returned slice will be empty — operation succeeded, but the caller
// must re-list to address the messages by id.
//
// Semantics on multi-ID input are fail-fast across folders. A MOVE is
// not idempotent: re-running after a partial failure will fail with
// "message not found" on the IDs already moved. Re-list to recover.
func (c *Client) MoveMessages(ctx context.Context, ids []mbxid.ID, dest string) ([]mbxid.ID, error) {
	return c.relocate(ids, dest, "MOVE", func(set imap.UIDSet) (uint32, imap.UIDSet, error) {
		data, err := c.c.Move(set, dest).Wait()
		if err != nil {
			return 0, nil, err
		}
		// MoveData.DestUIDs is typed as NumSet (the protocol allows seq
		// sets in some forms); on a UID-form MOVE/COPY-fallback the
		// runtime type is UIDSet. If it isn't, dest UIDs are unknown.
		destSet, _ := data.DestUIDs.(imap.UIDSet)
		return data.UIDValidity, destSet, nil
	})
}

// CopyMessages satisfies message.Copier.
//
// Like Move, but the source UIDs remain valid. Same UIDPLUS caveat
// applies to the returned IDs.
func (c *Client) CopyMessages(ctx context.Context, ids []mbxid.ID, dest string) ([]mbxid.ID, error) {
	return c.relocate(ids, dest, "COPY", func(set imap.UIDSet) (uint32, imap.UIDSet, error) {
		data, err := c.c.Copy(set, dest).Wait()
		if err != nil {
			return 0, nil, err
		}
		return data.UIDValidity, data.DestUIDs, nil
	})
}

// relocate runs the per-group SELECT + (MOVE|COPY) loop shared by
// MoveMessages and CopyMessages. The op callback issues the protocol
// command and returns (uidValidity, destUIDs) for mintDestIDs. opName
// is the human label that appears in wrapped errors.
func (c *Client) relocate(ids []mbxid.ID, dest, opName string, op func(imap.UIDSet) (uint32, imap.UIDSet, error)) ([]mbxid.ID, error) {
	groups, err := groupIDsByFolder(c.Account, ids)
	if err != nil {
		return nil, err
	}
	var out []mbxid.ID
	for _, group := range groups {
		first := group[0]
		if err := c.selectAndVerify(first); err != nil {
			return nil, err
		}
		set, err := buildUIDSet(group)
		if err != nil {
			return nil, err
		}
		uidValidity, destUIDs, err := op(set)
		if err != nil {
			return nil, fmt.Errorf("imap: %s from %q to %q: %w", opName, first.Folder, dest, err)
		}
		out = append(out, mintDestIDs(c.Account, dest, uidValidity, destUIDs)...)
	}
	return out, nil
}

// DeleteMessages satisfies message.Deleter.
//
// Default (`permanent=false`) is "move to trash". The trash folder is
// resolved from folder.aliases.trash on the account config; if unset,
// returns config.invalid so the user has to declare intent rather than
// mbx silently picking a folder.
//
// `permanent=true` skips trash entirely: STORE +FLAGS \Deleted, then
// UID EXPUNGE (or plain EXPUNGE on servers without UIDPLUS).
func (c *Client) DeleteMessages(ctx context.Context, ids []mbxid.ID, permanent bool) error {
	if !permanent {
		trash, err := c.resolveTrashFolder()
		if err != nil {
			return err
		}
		_, err = c.MoveMessages(ctx, ids, trash)
		return err
	}
	groups, err := groupIDsByFolder(c.Account, ids)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if err := c.permanentDeleteGroup(group); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) permanentDeleteGroup(group []mbxid.ID) error {
	first := group[0]
	if err := c.selectAndVerify(first); err != nil {
		return err
	}
	set, err := buildUIDSet(group)
	if err != nil {
		return err
	}
	if _, err := c.c.Store(set, &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagDeleted},
		Silent: true,
	}, nil).Collect(); err != nil {
		return fmt.Errorf("imap: STORE +FLAGS.SILENT \\Deleted in %q: %w", first.Folder, err)
	}
	if c.c.Caps().Has(imap.CapUIDPlus) {
		if _, err := c.c.UIDExpunge(set).Collect(); err != nil {
			return fmt.Errorf("imap: UID EXPUNGE in %q: %w", first.Folder, err)
		}
		return nil
	}
	if _, err := c.c.Expunge().Collect(); err != nil {
		return fmt.Errorf("imap: EXPUNGE in %q: %w", first.Folder, err)
	}
	return nil
}

// resolveTrashFolder reads folder.aliases.trash from the account config.
// Required for delete-to-trash; absent → config.invalid (the user must
// declare what "trash" means for their server: "Trash", "[Gmail]/Trash",
// "Deleted Items", …).
func (c *Client) resolveTrashFolder() (string, error) {
	if c.Cfg.Folder == nil || c.Cfg.Folder.Aliases["trash"] == "" {
		return "", output.Errorf(output.CodeConfigInvalid,
			"imap: folder.aliases.trash is unset for account %q; required for `message delete` without --permanent",
			c.Account).WithDetails("account", c.Account)
	}
	return c.Cfg.Folder.Aliases["trash"], nil
}

// buildUIDSet collects every id's UID into a single set. Caller is
// responsible for having grouped by folder + UIDVALIDITY first.
func buildUIDSet(group []mbxid.ID) (imap.UIDSet, error) {
	if len(group) == 0 {
		return nil, fmt.Errorf("imap: empty id group")
	}
	var set imap.UIDSet
	for _, id := range group[1:] {
		if id.UIDValidity != group[0].UIDValidity {
			return nil, output.Errorf(output.CodeProviderIDInvalidated,
				"imap: ids in folder %q span uidvalidity epochs (%d vs %d); re-list",
				group[0].Folder, group[0].UIDValidity, id.UIDValidity).
				WithDetails("folder", group[0].Folder)
		}
	}
	for _, id := range group {
		set.AddNum(imap.UID(id.UID))
	}
	return set, nil
}

// mintDestIDs builds mbx IDs for the messages that landed in dest. The
// dest UIDs come from the server's COPYUID/MOVE response; if the server
// didn't return them (no UIDPLUS / IMAP4rev2), we return an empty slice
// so the caller can tell it's a known-unknown rather than a silent loss.
func mintDestIDs(account, dest string, uidValidity uint32, destUIDs imap.UIDSet) []mbxid.ID {
	nums, ok := destUIDs.Nums()
	if !ok || len(nums) == 0 {
		return nil
	}
	out := make([]mbxid.ID, 0, len(nums))
	for _, u := range nums {
		out = append(out, mbxid.NewIMAP(account, dest, uidValidity, uint32(u)))
	}
	return out
}
