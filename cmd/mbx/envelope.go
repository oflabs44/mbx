package main

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/account"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/output"
)

func newEnvelopeCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "envelope",
		Short: "List, search, and inspect envelopes (cheap; no body fetch)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newEnvelopeListCmd(g, stdout, stderr),
		newEnvelopeSearchCmd(g, stdout, stderr),
		newEnvelopeFlagCmd(g, stdout, stderr),
		newEnvelopeThreadCmd(g, stdout, stderr),
	)
	return cmd
}

func newEnvelopeThreadCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	c := &cobra.Command{
		Use:   "thread <id>",
		Short: "Return the thread containing the given envelope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := mbxid.Parse(args[0])
			if err != nil {
				return output.Errorf(output.CodeUsageInvalid, "parsing id: %s", err.Error())
			}
			return runEnvelopeThread(cmd.Context(), g, stdout, stderr, id)
		},
	}
	return c
}

func runEnvelopeThread(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, id mbxid.ID) error {
	cname, acct, b, err := openBackendForID(ctx, g, id)
	if err != nil {
		return err
	}
	defer closeBackend(b)
	t, ok := b.(envelope.ThreadSearcher)
	if !ok {
		return unsupportedErr(acct, "threading")
	}
	thread, err := envelope.ThreadOf(ctx, t, envelope.ThreadQuery{ID: id})
	if err != nil {
		return err
	}
	meta := envelopeListMeta{AccountsQueried: []string{cname}}
	return output.NewWriter(stdout, stderr, g.format()).Success(thread, meta)
}

// envelopeListMeta is the meta block for envelope list/search responses.
// next_cursors is a per-account map; mbx single-account today and
// fanout-shaped from day one (phase 7 fills it).
type envelopeListMeta struct {
	AccountsQueried []string          `json:"accounts_queried"`
	NextCursors     map[string]string `json:"next_cursors,omitempty"`
	Errors          map[string]string `json:"errors,omitempty"`
}

type envelopeFlags struct {
	folder        string
	limit         int
	cursor        string
	unread        bool
	starred       bool
	hasAttachment bool
	from          string
	to            string
	after         string
	before        string
	rawQuery      string
}

func bindEnvelopeFlags(c *cobra.Command, ef *envelopeFlags) {
	f := c.Flags()
	f.StringVar(&ef.folder, "folder", "", "Filter to one folder. Default depends on verb.")
	f.IntVar(&ef.limit, "limit", 0, "Per-account limit (1..500, default 20).")
	f.StringVar(&ef.cursor, "cursor", "", "Resume from a previous response's meta.next_cursor.")
	f.BoolVar(&ef.unread, "unread", false, "Filter to unread (--unread=false for read).")
	f.BoolVar(&ef.starred, "starred", false, "Filter to starred.")
	f.BoolVar(&ef.hasAttachment, "has-attachment", false, "Filter to messages with attachments.")
	f.StringVar(&ef.from, "from", "", "From-address filter.")
	f.StringVar(&ef.to, "to", "", "To-address filter.")
	f.StringVar(&ef.after, "after", "", "ISO-8601 date (YYYY-MM-DD).")
	f.StringVar(&ef.before, "before", "", "ISO-8601 date (YYYY-MM-DD).")
	f.StringVar(&ef.rawQuery, "query", "", "Provider-native raw query (Gmail q= syntax / IMAP SEARCH).")
}

// toListQuery materializes flags into the normalized query, honouring the
// pflag "Changed" check so unset boolean filters stay nil (no filter)
// rather than being read as the zero value (false).
//
// Folder defaulting is deferred to the runX helpers below so they can
// consult the resolved account (folder.aliases.inbox vs the literal
// "INBOX") for `list`, while `search` keeps an empty Folder (cross-
// folder).
func (ef *envelopeFlags) toListQuery(c *cobra.Command) (envelope.ListQuery, error) {
	q := envelope.ListQuery{
		Folder:   ef.folder,
		Limit:    ef.limit,
		Cursor:   ef.cursor,
		From:     ef.from,
		To:       ef.to,
		RawQuery: ef.rawQuery,
	}
	if c.Flags().Changed("unread") {
		v := ef.unread
		q.Unread = &v
	}
	if c.Flags().Changed("starred") {
		v := ef.starred
		q.Starred = &v
	}
	if c.Flags().Changed("has-attachment") {
		v := ef.hasAttachment
		q.HasAttachment = &v
	}
	if ef.after != "" {
		t, err := time.Parse("2006-01-02", ef.after)
		if err != nil {
			return envelope.ListQuery{}, output.Errorf(output.CodeUsageInvalid, "--after must be YYYY-MM-DD: %s", err.Error())
		}
		q.After = t
	}
	if ef.before != "" {
		t, err := time.Parse("2006-01-02", ef.before)
		if err != nil {
			return envelope.ListQuery{}, output.Errorf(output.CodeUsageInvalid, "--before must be YYYY-MM-DD: %s", err.Error())
		}
		q.Before = t
	}
	return q, nil
}

func newEnvelopeListCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	ef := &envelopeFlags{}
	c := &cobra.Command{
		Use:   "list",
		Short: "List envelopes from one or more accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q, err := ef.toListQuery(cmd)
			if err != nil {
				return err
			}
			return runEnvelopeList(cmd.Context(), g, stdout, stderr, q)
		},
	}
	bindEnvelopeFlags(c, ef)
	return c
}

func newEnvelopeSearchCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	ef := &envelopeFlags{}
	c := &cobra.Command{
		Use:   "search <keywords>",
		Short: "Cross-folder keyword search",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := ef.toListQuery(cmd)
			if err != nil {
				return err
			}
			return runEnvelopeSearch(cmd.Context(), g, stdout, stderr, envelope.SearchQuery{
				ListQuery: q,
				Keywords:  args[0],
			})
		},
	}
	bindEnvelopeFlags(c, ef)
	return c
}

func runEnvelopeList(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, q envelope.ListQuery) error {
	acctName, acct, err := requireSingleAccount(g)
	if err != nil {
		return err
	}
	if q.Folder == "" {
		q.Folder = canonicalInbox(acct)
	}
	backend, err := newBackend(ctx, acctName, acct)
	if err != nil {
		return err
	}
	defer closeBackend(backend)
	page, err := envelope.List(ctx, backend, q)
	if err != nil {
		return err
	}
	return emitEnvelopePage(stdout, stderr, g, acctName, page)
}

func runEnvelopeSearch(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, q envelope.SearchQuery) error {
	acctName, acct, err := requireSingleAccount(g)
	if err != nil {
		return err
	}
	backend, err := newBackend(ctx, acctName, acct)
	if err != nil {
		return err
	}
	defer closeBackend(backend)
	page, err := envelope.Search(ctx, backend, q)
	if err != nil {
		return err
	}
	return emitEnvelopePage(stdout, stderr, g, acctName, page)
}

func emitEnvelopePage(stdout, stderr io.Writer, g *GlobalFlags, acctName string, page envelope.Page) error {
	meta := envelopeListMeta{AccountsQueried: []string{acctName}}
	if page.NextCursor != "" {
		meta.NextCursors = map[string]string{acctName: page.NextCursor}
	}
	return output.NewWriter(stdout, stderr, g.format()).Success(page.Envelopes, meta)
}

// canonicalInbox resolves the per-account inbox folder name. Honors
// folder.aliases.inbox when set (Gmail-via-IMAP, dovecot virtual
// folders, Migadu special namespaces); falls back to the IMAP-spec /
// Gmail label literal "INBOX" otherwise.
func canonicalInbox(acct *config.Account) string {
	if acct.Folder != nil {
		if v := acct.Folder.Aliases["inbox"]; v != "" {
			return v
		}
	}
	return "INBOX"
}

// flagResult is the JSON shape `mbx envelope flag` emits on success.
// Includes the IDs touched and the applied diff so callers can pipe it
// back into a downstream `envelope list` without re-deriving state.
type flagResult struct {
	IDs          []string `json:"ids"`
	FlagsAdded   []string `json:"flags_added,omitempty"`
	FlagsRemoved []string `json:"flags_removed,omitempty"`
}

func newEnvelopeFlagCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	var addRaw, removeRaw []string
	c := &cobra.Command{
		Use:   "flag <id>...",
		Short: "Add or remove flags on one or more envelopes",
		Long: "Apply a flag delta to one or more envelopes. Vocabulary: " +
			"seen, flagged, answered, draft, deleted. Gmail supports only " +
			"seen and flagged via this verb; IMAP supports the full set.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			add, err := parseFlagList(addRaw, "--add")
			if err != nil {
				return err
			}
			remove, err := parseFlagList(removeRaw, "--remove")
			if err != nil {
				return err
			}
			if len(add) == 0 && len(remove) == 0 {
				return output.Errorf(output.CodeInputMissingFlag,
					"envelope flag: pass at least one of --add or --remove")
			}
			ids, err := parseSharedAccountIDs(args)
			if err != nil {
				return err
			}
			return runEnvelopeFlag(cmd.Context(), g, stdout, stderr, ids, add, remove)
		},
	}
	c.Flags().StringSliceVar(&addRaw, "add", nil, "Flag(s) to add. Repeatable or comma-separated. Vocabulary: seen, flagged, answered, draft, deleted.")
	c.Flags().StringSliceVar(&removeRaw, "remove", nil, "Flag(s) to remove. Same vocabulary as --add.")
	return c
}

// parseFlagList normalizes the raw StringSliceVar values (which cobra has
// already split on commas and accumulated across repeats) into the typed
// envelope.Flag vocabulary. Empty entries are dropped; unknown names
// surface a stable usage.invalid error naming the offending flag.
func parseFlagList(raw []string, flagName string) ([]envelope.Flag, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]envelope.Flag, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		f, err := envelope.ParseFlag(p)
		if err != nil {
			return nil, output.Errorf(output.CodeUsageInvalid, "%s: %s", flagName, err.Error())
		}
		out = append(out, f)
	}
	return out, nil
}

// parseSharedAccountIDs parses positional ID args and rejects mixed-
// account input. Cross-account flagging is a phase-7 fanout concern; for
// now the command takes IDs from one account at a time.
func parseSharedAccountIDs(args []string) ([]mbxid.ID, error) {
	ids := make([]mbxid.ID, 0, len(args))
	for i, a := range args {
		id, err := mbxid.Parse(a)
		if err != nil {
			return nil, output.Errorf(output.CodeUsageInvalid, "parsing id %d: %s", i+1, err.Error())
		}
		if len(ids) > 0 && id.Account != ids[0].Account {
			return nil, output.Errorf(output.CodeUsageInvalid,
				"all ids must share an account (id %d has account %q, expected %q)",
				i+1, id.Account, ids[0].Account)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func runEnvelopeFlag(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, ids []mbxid.ID, add, remove []envelope.Flag) error {
	cname, acct, b, err := openBackendForID(ctx, g, ids[0])
	if err != nil {
		return err
	}
	defer closeBackend(b)
	flagger, ok := b.(envelope.Flagger)
	if !ok {
		return unsupportedErr(acct, "flagging")
	}
	if err := envelope.ApplyFlags(ctx, flagger, ids, add, remove); err != nil {
		return err
	}
	data := flagResult{
		IDs:          canonicalIDStrings(ids, cname),
		FlagsAdded:   flagsToStrings(add),
		FlagsRemoved: flagsToStrings(remove),
	}
	meta := envelopeListMeta{AccountsQueried: []string{cname}}
	return output.NewWriter(stdout, stderr, g.format()).Success(data, meta)
}

func flagsToStrings(in []envelope.Flag) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, f := range in {
		out[i] = string(f)
	}
	return out
}

// requireSingleAccount enforces phase-2's single-account scope. Phase 7
// will replace this with a fan-out helper.
func requireSingleAccount(g *GlobalFlags) (string, *config.Account, error) {
	if len(g.Accounts) == 0 {
		return "", nil, output.Errorf(output.CodeInputMissingFlag, "missing required flag -a/--account")
	}
	if len(g.Accounts) > 1 {
		return "", nil, output.Errorf(output.CodeUsageInvalid,
			"multi-account fanout lands in phase 7; pass exactly one -a for now (got %d)", len(g.Accounts))
	}
	name := g.Accounts[0]

	c, err := loadConfig(g)
	if err != nil {
		return "", nil, err
	}
	cname, acct, err := account.Lookup(c, name)
	if err != nil {
		return "", nil, output.Errorf(output.CodeConfigUnknownAccount, "%s", err.Error()).
			WithDetails("account", name)
	}
	return cname, acct, nil
}
