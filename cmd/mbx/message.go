package main

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/oflabs44/mbx/internal/account"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/mbxid"
	"github.com/oflabs44/mbx/internal/message"
	"github.com/oflabs44/mbx/internal/output"
)

func newMessageCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "Read and export full message bodies",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newMessageReadCmd(g, stdout, stderr),
		newMessageExportCmd(g, stdout, stderr),
		newMessageMoveCmd(g, stdout, stderr),
		newMessageCopyCmd(g, stdout, stderr),
		newMessageDeleteCmd(g, stdout, stderr),
		newMessageArchiveCmd(g, stdout, stderr),
		newMessageSendCmd(g, stdout, stderr),
		newMessageReplyCmd(g, stdout, stderr),
		newMessageForwardCmd(g, stdout, stderr),
	)
	return cmd
}

func newMessageReadCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	var (
		htmlBody     bool
		raw          bool
		preview      bool
		extraHeaders []string
		noHeaders    bool
	)
	c := &cobra.Command{
		Use:   "read <id>",
		Short: "Read a message by mbx ID",
		Example: `  mbx message read gmail:work:18f3...
  mbx message read gmail:work:18f3... --preview
  mbx message read gmail:work:18f3... --html -H Message-ID`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if htmlBody && raw {
				return output.Errorf(output.CodeUsageInvalid, "--html and --raw are mutually exclusive")
			}
			id, err := mbxid.Parse(args[0])
			if err != nil {
				return output.Errorf(output.CodeUsageInvalid, "parsing id: %s", err.Error())
			}
			opt := message.ReadOptions{
				PreferHTML:     htmlBody,
				IncludeRaw:     raw,
				IncludeHeaders: extraHeaders,
				OmitHeaders:    noHeaders,
				MarkSeen:       !preview,
			}
			return runMessageRead(cmd.Context(), g, stdout, stderr, id, opt)
		},
	}
	c.Flags().BoolVar(&htmlBody, "html", false, "Return raw HTML body instead of plain.")
	c.Flags().BoolVar(&raw, "raw", false, "Return full MIME parts (replaces body field).")
	c.Flags().BoolVar(&preview, "preview", false, "Don't mark as seen.")
	c.Flags().StringArrayVarP(&extraHeaders, "header", "H", nil, "Include the named header. Repeatable.")
	c.Flags().BoolVar(&noHeaders, "no-headers", false, "Omit the entire headers section.")
	return c
}

func newMessageExportCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:     "export <id>",
		Short:   "Dump raw RFC 5322 bytes to stdout (no JSON envelope)",
		Example: `  mbx message export gmail:work:18f3... > saved.eml`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := mbxid.Parse(args[0])
			if err != nil {
				return output.Errorf(output.CodeUsageInvalid, "parsing id: %s", err.Error())
			}
			return runMessageExport(cmd.Context(), g, stdout, stderr, id)
		},
	}
}

func runMessageRead(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, id mbxid.ID, opt message.ReadOptions) error {
	cname, _, b, err := openBackendForID(ctx, g, id)
	if err != nil {
		return err
	}
	defer closeBackend(b)
	msg, err := b.ReadMessage(ctx, id, opt)
	if err != nil {
		return err
	}
	msg.Account = cname
	return output.NewWriter(stdout, stderr, g.format()).Success(msg, nil)
}

func runMessageExport(ctx context.Context, g *GlobalFlags, stdout, _ io.Writer, id mbxid.ID) error {
	_, _, b, err := openBackendForID(ctx, g, id)
	if err != nil {
		return err
	}
	defer closeBackend(b)
	raw, err := b.ReadMessageRaw(ctx, id)
	if err != nil {
		return err
	}
	// Deliberate carve-out from ADR-0004: export emits raw bytes to stdout
	// so it can be piped into msmtp / mu / .eml workflows.
	_, err = stdout.Write(raw)
	return err
}

// mutateResult is the JSON shape `mbx message move`, `copy`, and `delete`
// emit on success. Move/copy populate NewIDs (empty on IMAP servers
// without UIDPLUS — see commands.md); delete leaves it empty.
type mutateResult struct {
	IDs    []string `json:"ids"`
	NewIDs []string `json:"new_ids,omitempty"`
	Dest   string   `json:"dest,omitempty"`
}

func newMessageMoveCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	c := &cobra.Command{
		Use:   "move <id>... <folder>",
		Short: "Move one or more messages to a destination folder",
		Example: `  mbx message move gmail:work:18f3... Newsletters
  mbx message move imap:work:INBOX:1:42 imap:work:INBOX:1:43 Archive`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, dest, err := parseMutateArgs(args)
			if err != nil {
				return err
			}
			return runMessageMove(cmd.Context(), g, stdout, stderr, ids, dest)
		},
	}
	return c
}

func newMessageCopyCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	c := &cobra.Command{
		Use:     "copy <id>... <folder>",
		Short:   "Copy one or more messages to a destination folder",
		Example: `  mbx message copy gmail:work:18f3... "Saved"`,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, dest, err := parseMutateArgs(args)
			if err != nil {
				return err
			}
			return runMessageCopy(cmd.Context(), g, stdout, stderr, ids, dest)
		},
	}
	return c
}

func newMessageArchiveCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	c := &cobra.Command{
		Use:   "archive <id>...",
		Short: "Archive one or more messages",
		Long: "Gmail: removes the INBOX label. IMAP: moves to folder.aliases.archive " +
			"(config.invalid if unset). See ADR-0009.",
		Example: `  mbx message archive gmail:work:18f3...
  mbx message archive imap:work:INBOX:1:42 imap:work:INBOX:1:43`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseSharedAccountIDs(args)
			if err != nil {
				return err
			}

			return runMessageArchive(cmd.Context(), g, stdout, stderr, ids)
		},
	}
	return c
}

func newMessageDeleteCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	var permanent bool
	c := &cobra.Command{
		Use:   "delete <id>...",
		Short: "Delete one or more messages (default: move to trash)",
		Long: "Default behaviour moves messages to the account's trash folder. " +
			"--permanent bypasses trash and hard-deletes (irreversible).",
		Example: `  mbx message delete gmail:work:18f3...
  mbx message delete imap:work:INBOX:1:42 --permanent`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := parseSharedAccountIDs(args)
			if err != nil {
				return err
			}
			return runMessageDelete(cmd.Context(), g, stdout, stderr, ids, permanent)
		},
	}
	c.Flags().BoolVar(&permanent, "permanent", false, "Skip trash and hard-delete.")
	return c
}

// parseMutateArgs splits the trailing positional <folder> from the leading
// id list, then validates the ids share an account. The cobra Args check
// already enforces at least 2 positional, so len(args)-1 is the dest.
func parseMutateArgs(args []string) ([]mbxid.ID, string, error) {
	dest := args[len(args)-1]
	if strings.TrimSpace(dest) == "" {
		return nil, "", output.Errorf(output.CodeUsageInvalid, "destination folder must not be empty")
	}
	ids, err := parseSharedAccountIDs(args[:len(args)-1])
	if err != nil {
		return nil, "", err
	}
	return ids, dest, nil
}

func runMessageMove(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, ids []mbxid.ID, dest string) error {
	cname, acct, b, err := openBackendForID(ctx, g, ids[0])
	if err != nil {
		return err
	}
	defer closeBackend(b)
	mover, ok := b.(message.Mover)
	if !ok {
		return unsupportedErr(acct, "move")
	}
	newIDs, err := message.Move(ctx, mover, ids, dest)
	if err != nil {
		return err
	}
	cacheInvalidateAfterMutation(ctx, g, stderr, ids, cname)
	return emitMutateResult(stdout, stderr, g, cname, ids, newIDs, dest)
}

func runMessageCopy(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, ids []mbxid.ID, dest string) error {
	cname, acct, b, err := openBackendForID(ctx, g, ids[0])
	if err != nil {
		return err
	}
	defer closeBackend(b)
	copier, ok := b.(message.Copier)
	if !ok {
		return unsupportedErr(acct, "copy")
	}
	newIDs, err := message.Copy(ctx, copier, ids, dest)
	if err != nil {
		return err
	}
	return emitMutateResult(stdout, stderr, g, cname, ids, newIDs, dest)
}

func runMessageArchive(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, ids []mbxid.ID) error {
	cname, acct, b, err := openBackendForID(ctx, g, ids[0])
	if err != nil {
		return err
	}
	defer closeBackend(b)
	archiver, ok := b.(message.Archiver)
	if !ok {
		return unsupportedErr(acct, "archive")
	}
	newIDs, dest, err := message.Archive(ctx, archiver, ids)
	if err != nil {
		return err
	}
	cacheInvalidateAfterMutation(ctx, g, stderr, ids, cname)

	return emitMutateResult(stdout, stderr, g, cname, ids, newIDs, dest)
}

func runMessageDelete(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, ids []mbxid.ID, permanent bool) error {
	cname, acct, b, err := openBackendForID(ctx, g, ids[0])
	if err != nil {
		return err
	}
	defer closeBackend(b)
	deleter, ok := b.(message.Deleter)
	if !ok {
		return unsupportedErr(acct, "delete")
	}
	if err := message.Delete(ctx, deleter, ids, permanent); err != nil {
		return err
	}
	cacheInvalidateAfterMutation(ctx, g, stderr, ids, cname)
	return emitMutateResult(stdout, stderr, g, cname, ids, nil, "")
}

// openBackendForID resolves the account from an mbx ID and opens the
// backend. Pulled out so the three message-mutate handlers don't each
// open-code the same 5-line prologue. Returns the canonical account name
// (ADR-0007) alongside the account and backend so callers can stamp
// echoed IDs and meta fields with the stable form. Caller defers
// closeBackend on b.
func openBackendForID(ctx context.Context, g *GlobalFlags, id mbxid.ID) (string, *config.Account, backend, error) {
	cname, acct, err := lookupAccountForID(g, id)
	if err != nil {
		return "", nil, nil, err
	}
	b, err := newBackend(ctx, cname, acct)
	if err != nil {
		return "", nil, nil, err
	}
	return cname, acct, b, nil
}

func unsupportedErr(acct *config.Account, verb string) error {
	return output.Errorf(output.CodeProviderUnsupported,
		"backend %q does not support %s", acct.Backend.Type, verb)
}

func emitMutateResult(stdout, stderr io.Writer, g *GlobalFlags, cname string, ids, newIDs []mbxid.ID, dest string) error {
	// ids are user-typed (possibly alias-form); newIDs come from the server
	// (already canonical via the backend's c.Account stamping).
	data := mutateResult{
		IDs:    canonicalIDStrings(ids, cname),
		NewIDs: idsToStrings(newIDs),
		Dest:   dest,
	}
	meta := envelopeListMeta{AccountsQueried: []string{cname}}
	return output.NewWriter(stdout, stderr, g.format()).Success(data, meta)
}

// canonicalIDStrings stringifies ids after stamping each one's Account
// segment with cname. For input IDs reaching an echo verb (envelope flag,
// message move/copy/delete), this enforces the canonical-on-emit rule of
// ADR-0007 — the user may have passed an alias-form ID, but the response
// always carries the canonical form so downstream stored references
// migrate naturally.
func canonicalIDStrings(ids []mbxid.ID, cname string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		id.Account = cname
		out[i] = id.String()
	}
	return out
}

func idsToStrings(ids []mbxid.ID) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// bodyFlags is the body-source + presentation triplet shared by send,
// reply, and forward. Embedded into each verb's flag struct; registered
// once via registerBodyFlags so the cobra wiring stays consistent.
type bodyFlags struct {
	body      string
	bodyFile  string
	bodyStdin bool
	html      bool
	attach    []string
	replyTo   string
}

func registerBodyFlags(f *pflag.FlagSet, bf *bodyFlags) {
	f.StringVar(&bf.body, "body", "", "Inline body text. Mutually exclusive with --body-file / --body-stdin.")
	f.StringVar(&bf.bodyFile, "body-file", "", "Read body from file.")
	f.BoolVar(&bf.bodyStdin, "body-stdin", false, "Read body from stdin.")
	f.BoolVar(&bf.html, "html", false, "Treat body as HTML (Content-Type: text/html).")
	f.StringArrayVar(&bf.attach, "attach", nil, "Attach a file by path. Repeatable.")
	f.StringVar(&bf.replyTo, "reply-to", "", "Override Reply-To header.")
}

// sendFlags collects all `mbx message send` CLI inputs. The three body
// sources are populated only when their respective flag is set; the
// handler enforces exactly-one-of via input.ambiguous_body.
type sendFlags struct {
	bodyFlags
	to      []string
	cc      []string
	bcc     []string
	subject string
}

func newMessageSendCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	sf := &sendFlags{}
	c := &cobra.Command{
		Use:   "send",
		Short: "Compose and send a message",
		Example: `  mbx message send -a work --to alex@x.com --subject "Status" --body "Up 4.3%."
  mbx message send -a work --to a@x --cc b@y --subject "Notes" --body-stdin <<<"..."`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMessageSend(cmd.Context(), g, stdout, stderr, sf)
		},
	}
	f := c.Flags()
	f.StringArrayVar(&sf.to, "to", nil, "Recipient address. Repeatable. Required.")
	f.StringArrayVar(&sf.cc, "cc", nil, "Cc address. Repeatable.")
	f.StringArrayVar(&sf.bcc, "bcc", nil, "Bcc address. Repeatable.")
	f.StringVar(&sf.subject, "subject", "", "Subject line. Required.")
	registerBodyFlags(f, &sf.bodyFlags)
	return c
}

// sendResult is the JSON shape `mbx message send` emits on success. The
// `from` field reflects what was actually sent (so callers can confirm
// the account's configured email landed in the right field); subject and
// recipients echo back the inputs for traceability.
type sendResult struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Cc      []string `json:"cc,omitempty"`
	Bcc     []string `json:"bcc,omitempty"`
	Subject string   `json:"subject"`
}

func runMessageSend(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, sf *sendFlags) error {
	if sf.subject == "" {
		return output.Errorf(output.CodeInputMissingFlag, "--subject is required")
	}
	if len(sf.to) == 0 {
		return output.Errorf(output.CodeInputMissingFlag, "--to is required (at least once)")
	}
	body, err := readBody(os.Stdin, sf.body, sf.bodyFile, sf.bodyStdin)
	if err != nil {
		return err
	}

	acctName, acct, err := requireSingleAccount(g)
	if err != nil {
		return err
	}
	if strings.TrimSpace(acct.Email) == "" {
		return output.Errorf(output.CodeConfigInvalid,
			"account %q has no `email` field set; required for send", acctName)
	}

	sender, err := newSendBackend(ctx, acctName, acct)
	if err != nil {
		return err
	}
	defer closeBackend(sender)

	spec := message.ComposeSpec{
		From:    acct.Email,
		To:      sf.to,
		Cc:      sf.cc,
		Bcc:     sf.bcc,
		Subject: sf.subject,
		Body:    body,
		HTML:    sf.html,
		ReplyTo: sf.replyTo,
		Attach:  toAttachmentSpecs(sf.attach),
	}
	return composeAndSend(ctx, stdout, stderr, g, acctName, sender, spec)
}

// readBody enforces exactly-one-of {--body, --body-file, --body-stdin}
// and returns the resolved body bytes as a string. Shared by send,
// reply, and forward — same flag triplet on each verb.
func readBody(in io.Reader, body, bodyFile string, bodyStdin bool) (string, error) {
	sources := 0
	if body != "" {
		sources++
	}
	if bodyFile != "" {
		sources++
	}
	if bodyStdin {
		sources++
	}
	switch sources {
	case 0:
		return "", output.Errorf(output.CodeInputMissingFlag,
			"exactly one of --body / --body-file / --body-stdin is required")
	case 1:
	default:
		return "", output.Errorf(output.CodeInputAmbiguous,
			"only one of --body / --body-file / --body-stdin may be set")
	}
	switch {
	case body != "":
		return body, nil
	case bodyFile != "":
		b, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", output.Errorf(output.CodeUsageInvalid, "--body-file: %s", err.Error())
		}
		return string(b), nil
	default:
		if isTerminal(os.Stdin) {
			return "", output.Errorf(output.CodeUsageInvalid,
				"--body-stdin requires piped or redirected input; from a terminal this would block forever. Use --body or --body-file instead.")
		}
		b, err := io.ReadAll(in)
		if err != nil {
			return "", output.Errorf(output.CodeUsageInvalid, "--body-stdin: %s", err.Error())
		}
		return string(b), nil
	}
}

// isTerminal reports whether f is attached to a character device. Used
// to refuse --body-stdin against an interactive shell (which would block
// io.ReadAll forever). Stdlib-only (no x/term dependency).
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func toAttachmentSpecs(paths []string) []message.AttachmentSpec {
	if len(paths) == 0 {
		return nil
	}
	out := make([]message.AttachmentSpec, 0, len(paths))
	for _, p := range paths {
		out = append(out, message.AttachmentSpec{Path: p})
	}
	return out
}

// lookupAccountForID resolves an account from an mbx ID. The ID encodes
// the account name (canonical or alias); -a is optional but if present
// must resolve to the same canonical account. Returns the canonical
// name so callers stamping new mbx IDs use the stable form (ADR-0007).
func lookupAccountForID(g *GlobalFlags, id mbxid.ID) (string, *config.Account, error) {
	if len(g.Accounts) > 1 {
		return "", nil, output.Errorf(output.CodeUsageInvalid,
			"single-message commands take at most one -a (got %d)", len(g.Accounts))
	}
	c, err := loadConfig(g)
	if err != nil {
		return "", nil, err
	}
	cname, acct, err := account.Lookup(c, id.Account)
	if err != nil {
		return "", nil, output.Errorf(output.CodeConfigUnknownAccount, "%s", err.Error()).
			WithDetails("account", id.Account)
	}
	if len(g.Accounts) == 1 {
		flagCname, _, ok := c.Resolve(g.Accounts[0])
		if !ok || flagCname != cname {
			return "", nil, output.Errorf(output.CodeUsageInvalid,
				"-a %q does not resolve to the account encoded in the id (%q)", g.Accounts[0], id.Account)
		}
	}
	return cname, acct, nil
}

// replyFlags / forwardFlags embed bodyFlags (shared with sendFlags) and
// add the verb-specific knobs reply (--all, --quote) and forward
// (--to/cc/bcc) need.
type replyFlags struct {
	bodyFlags
	all   bool
	quote bool
}

type forwardFlags struct {
	bodyFlags
	to  []string
	cc  []string
	bcc []string
}

func newMessageReplyCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	rf := &replyFlags{}
	c := &cobra.Command{
		Use:   "reply <id>",
		Short: "Reply to a message; To/References/In-Reply-To are derived from the source",
		Example: `  mbx message reply gmail:work:18f3... --body "Acknowledged."
  mbx message reply gmail:work:18f3... --all --quote --body-stdin <<<"..."`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := mbxid.Parse(args[0])
			if err != nil {
				return output.Errorf(output.CodeUsageInvalid, "parsing id: %s", err.Error())
			}
			return runMessageReply(cmd.Context(), g, stdout, stderr, id, rf)
		},
	}
	f := c.Flags()
	f.BoolVar(&rf.all, "all", false, "Reply to all (To becomes source.From; Cc becomes source.To+Cc minus self).")
	f.BoolVar(&rf.quote, "quote", false, "Include the source body quoted below the new body.")
	registerBodyFlags(f, &rf.bodyFlags)
	return c
}

func newMessageForwardCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	ff := &forwardFlags{}
	c := &cobra.Command{
		Use:     "forward <id>",
		Short:   "Forward a message; To is required, original is quoted below the new body",
		Example: `  mbx message forward gmail:work:18f3... --to colleague@company.com --body "FYI"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := mbxid.Parse(args[0])
			if err != nil {
				return output.Errorf(output.CodeUsageInvalid, "parsing id: %s", err.Error())
			}
			return runMessageForward(cmd.Context(), g, stdout, stderr, id, ff)
		},
	}
	f := c.Flags()
	f.StringArrayVar(&ff.to, "to", nil, "Recipient address. Repeatable. Required.")
	f.StringArrayVar(&ff.cc, "cc", nil, "Cc address. Repeatable.")
	f.StringArrayVar(&ff.bcc, "bcc", nil, "Bcc address. Repeatable.")
	registerBodyFlags(f, &ff.bodyFlags)
	return c
}

func runMessageReply(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, id mbxid.ID, rf *replyFlags) error {
	body, err := readBody(os.Stdin, rf.body, rf.bodyFile, rf.bodyStdin)
	if err != nil {
		return err
	}
	rs, err := openSourceAndSender(ctx, g, id)
	if err != nil {
		return err
	}
	defer rs.close()

	spec, err := message.BuildReply(rs.source, message.ReplyOpts{
		From:    rs.from,
		Body:    body,
		HTML:    rf.html,
		All:     rf.all,
		Quote:   rf.quote,
		ReplyTo: rf.replyTo,
		Attach:  toAttachmentSpecs(rf.attach),
	})
	if err != nil {
		return output.Errorf(output.CodeUsageInvalid, "%s", err.Error())
	}
	return composeAndSend(ctx, stdout, stderr, g, rs.cname, rs.sender, spec)
}

func runMessageForward(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, id mbxid.ID, ff *forwardFlags) error {
	if len(ff.to) == 0 {
		return output.Errorf(output.CodeInputMissingFlag, "--to is required (at least once)")
	}
	body, err := readBody(os.Stdin, ff.body, ff.bodyFile, ff.bodyStdin)
	if err != nil {
		return err
	}
	rs, err := openSourceAndSender(ctx, g, id)
	if err != nil {
		return err
	}
	defer rs.close()

	spec, err := message.BuildForward(rs.source, message.ForwardOpts{
		From:    rs.from,
		To:      ff.to,
		Cc:      ff.cc,
		Bcc:     ff.bcc,
		Body:    body,
		HTML:    ff.html,
		ReplyTo: ff.replyTo,
		Attach:  toAttachmentSpecs(ff.attach),
	})
	if err != nil {
		return output.Errorf(output.CodeUsageInvalid, "%s", err.Error())
	}
	return composeAndSend(ctx, stdout, stderr, g, rs.cname, rs.sender, spec)
}

// replySession bundles what reply/forward need: the source message, the
// resolved From address, a Sender backend, the canonical account name
// (ADR-0007) for output stamping, and a single close for both the read-
// and send-side backends.
type replySession struct {
	source message.Message
	from   string
	cname  string
	sender message.Sender
	close  func()
}

// openSourceAndSender opens both the read-side backend (to fetch the
// source message) and the send-side backend. The read fetches headers
// required for threading (Message-ID, In-Reply-To, References) and the
// body for quoting. We don't mark the source seen — replying isn't reading.
func openSourceAndSender(ctx context.Context, g *GlobalFlags, id mbxid.ID) (replySession, error) {
	cname, acct, readB, err := openBackendForID(ctx, g, id)
	if err != nil {
		return replySession{}, err
	}
	if strings.TrimSpace(acct.Email) == "" {
		closeBackend(readB)
		return replySession{}, output.Errorf(output.CodeConfigInvalid,
			"account %q has no `email` field set; required for reply/forward", cname)
	}
	source, err := readB.ReadMessage(ctx, id, message.ReadOptions{
		IncludeHeaders: []string{"Message-ID", "In-Reply-To", "References"},
		MarkSeen:       false,
	})
	if err != nil {
		closeBackend(readB)
		return replySession{}, err
	}
	source.Account = cname

	sender, err := newSendBackend(ctx, cname, acct)
	if err != nil {
		closeBackend(readB)
		return replySession{}, err
	}
	return replySession{
		source: source,
		from:   acct.Email,
		cname:  cname,
		sender: sender,
		close: func() {
			closeBackend(sender)
			closeBackend(readB)
		},
	}, nil
}

// composeAndSend is the shared tail of runMessageReply / runMessageForward.
// Both verbs build a ComposeSpec from their inputs, then hand it through
// the same compose + send pipeline send uses.
func composeAndSend(ctx context.Context, stdout, stderr io.Writer, g *GlobalFlags, acctName string, sender message.Sender, spec message.ComposeSpec) error {
	outgoing, err := message.Compose(spec)
	if err != nil {
		return output.Errorf(output.CodeUsageInvalid, "compose: %s", err.Error())
	}
	if err := message.Send(ctx, sender, outgoing); err != nil {
		return err
	}
	data := sendResult{From: outgoing.From, To: spec.To, Cc: spec.Cc, Bcc: spec.Bcc, Subject: spec.Subject}
	meta := envelopeListMeta{AccountsQueried: []string{acctName}}
	return output.NewWriter(stdout, stderr, g.format()).Success(data, meta)
}
