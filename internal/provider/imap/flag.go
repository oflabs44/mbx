package imap

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"

	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/output"
)

// FlagEnvelopes satisfies envelope.Flagger. STORE applies within the
// selected mailbox, so IDs are grouped by folder and a SELECT+STORE pair
// runs per group. UIDVALIDITY mismatches surface as provider.id_invalidated.
//
// IMAP supports the full mbx flag vocabulary one-for-one (\Seen, \Flagged,
// \Answered, \Draft, \Deleted).
//
// Semantics on multi-ID input are fail-fast within a folder and across
// folders: the first STORE that fails aborts the rest. Single-flag adds
// (seen, flagged) are idempotent at the protocol level, so a retry of
// the same command after a transient failure is safe.
func (c *Client) FlagEnvelopes(ctx context.Context, ids []mbxid.ID, add, remove []envelope.Flag) error {
	addFlags, err := mapFlagsToIMAP(add)
	if err != nil {
		return err
	}
	removeFlags, err := mapFlagsToIMAP(remove)
	if err != nil {
		return err
	}

	groups, err := groupIDsByFolder(c.Account, ids)
	if err != nil {
		return err
	}

	for _, group := range groups {
		if err := c.storeFlagsInFolder(group, addFlags, removeFlags); err != nil {
			return err
		}
	}
	return nil
}

// storeFlagsInFolder SELECTs the folder for the group's first ID
// (validating UIDVALIDITY), confirms every other id in the group shares
// the same UIDVALIDITY, then issues one STORE per non-empty diff.
func (c *Client) storeFlagsInFolder(group []mbxid.ID, addFlags, removeFlags []imap.Flag) error {
	first := group[0]
	if err := c.selectAndVerify(first); err != nil {
		return err
	}
	set, err := buildUIDSet(group)
	if err != nil {
		return err
	}
	if len(addFlags) > 0 {
		if _, err := c.c.Store(set, &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Flags:  addFlags,
			Silent: true,
		}, nil).Collect(); err != nil {
			return fmt.Errorf("imap: STORE +FLAGS in %q: %w", first.Folder, err)
		}
	}
	if len(removeFlags) > 0 {
		if _, err := c.c.Store(set, &imap.StoreFlags{
			Op:     imap.StoreFlagsDel,
			Flags:  removeFlags,
			Silent: true,
		}, nil).Collect(); err != nil {
			return fmt.Errorf("imap: STORE -FLAGS in %q: %w", first.Folder, err)
		}
	}
	return nil
}

// groupIDsByFolder partitions ids by folder. Returns groups in insertion
// order so the first id within each group is deterministic (it carries
// the UIDVALIDITY checked at SELECT). Also validates that every id is an
// imap id for the right account — these conditions should already be
// enforced upstream by the cmd handler, but the backend is the trust
// boundary for its own provider/account pairing.
func groupIDsByFolder(account string, ids []mbxid.ID) ([][]mbxid.ID, error) {
	index := map[string]int{}
	var groups [][]mbxid.ID
	for _, id := range ids {
		if id.Provider != mbxid.IMAP {
			return nil, fmt.Errorf("imap: id %q is not an imap id", id.String())
		}
		if id.Account != account {
			return nil, fmt.Errorf("imap: id account %q does not match client %q", id.Account, account)
		}
		idx, ok := index[id.Folder]
		if !ok {
			index[id.Folder] = len(groups)
			groups = append(groups, []mbxid.ID{id})
			continue
		}
		groups[idx] = append(groups[idx], id)
	}
	return groups, nil
}

func mapFlagsToIMAP(in []envelope.Flag) ([]imap.Flag, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]imap.Flag, 0, len(in))
	for _, f := range in {
		switch f {
		case envelope.FlagSeen:
			out = append(out, imap.FlagSeen)
		case envelope.FlagFlagged:
			out = append(out, imap.FlagFlagged)
		case envelope.FlagAnswered:
			out = append(out, imap.FlagAnswered)
		case envelope.FlagDraft:
			out = append(out, imap.FlagDraft)
		case envelope.FlagDeleted:
			out = append(out, imap.FlagDeleted)
		default:
			return nil, output.Errorf(output.CodeProviderUnsupported,
				"imap: unsupported flag %q", f)
		}
	}
	return out, nil
}
