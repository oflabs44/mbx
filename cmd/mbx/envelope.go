package main

import (
	"context"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/account"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/envelope"
	"github.com/oflabs44/mbx/internal/output"
)

// gmailDefaultFolder matches the documented default for `mbx envelope list`
// when --folder is unspecified. Search defaults to no folder filter.
const gmailDefaultFolder = "INBOX"

func newEnvelopeCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "envelope",
		Short: "List, search, and inspect envelopes (cheap; no body fetch)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newEnvelopeListCmd(g, stdout, stderr),
		newEnvelopeSearchCmd(g, stdout, stderr),
	)
	return cmd
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
func (ef *envelopeFlags) toListQuery(c *cobra.Command, defaultFolder string) (envelope.ListQuery, error) {
	q := envelope.ListQuery{
		Folder:   ef.folder,
		Limit:    ef.limit,
		Cursor:   ef.cursor,
		From:     ef.from,
		To:       ef.to,
		RawQuery: ef.rawQuery,
	}
	if q.Folder == "" {
		q.Folder = defaultFolder
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
			q, err := ef.toListQuery(cmd, gmailDefaultFolder)
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
			q, err := ef.toListQuery(cmd, "")
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
	backend, err := newBackend(ctx, acctName, acct)
	if err != nil {
		return err
	}
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
	acct, err := account.Lookup(c, name)
	if err != nil {
		return "", nil, output.Errorf(output.CodeConfigUnknownAccount, "%s", err.Error()).
			WithDetails("account", name)
	}
	return name, acct, nil
}
