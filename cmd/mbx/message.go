package main

import (
	"context"
	"io"

	"github.com/spf13/cobra"

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
		Args:  cobra.ExactArgs(1),
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
		Use:   "export <id>",
		Short: "Dump raw RFC 5322 bytes to stdout (no JSON envelope)",
		Args:  cobra.ExactArgs(1),
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
	acct, err := lookupAccountForID(g, id)
	if err != nil {
		return err
	}
	backend, err := newBackend(ctx, id.Account, acct)
	if err != nil {
		return err
	}
	msg, err := backend.ReadMessage(ctx, id, opt)
	if err != nil {
		return err
	}
	msg.Account = id.Account
	return output.NewWriter(stdout, stderr, g.format()).Success(msg, nil)
}

func runMessageExport(ctx context.Context, g *GlobalFlags, stdout, _ io.Writer, id mbxid.ID) error {
	acct, err := lookupAccountForID(g, id)
	if err != nil {
		return err
	}
	backend, err := newBackend(ctx, id.Account, acct)
	if err != nil {
		return err
	}
	raw, err := backend.ReadMessageRaw(ctx, id)
	if err != nil {
		return err
	}
	// Deliberate carve-out from ADR-0004: export emits raw bytes to stdout
	// so it can be piped into msmtp / mu / .eml workflows.
	_, err = stdout.Write(raw)
	return err
}

// lookupAccountForID resolves an account from an mbx ID. The ID encodes
// the account name; -a is optional but if present must agree.
func lookupAccountForID(g *GlobalFlags, id mbxid.ID) (*config.Account, error) {
	if len(g.Accounts) > 1 {
		return nil, output.Errorf(output.CodeUsageInvalid,
			"single-message commands take at most one -a (got %d)", len(g.Accounts))
	}
	if len(g.Accounts) == 1 && g.Accounts[0] != id.Account {
		return nil, output.Errorf(output.CodeUsageInvalid,
			"-a %q does not match the account encoded in the id (%q)", g.Accounts[0], id.Account)
	}
	c, err := loadConfig(g)
	if err != nil {
		return nil, err
	}
	acct, err := account.Lookup(c, id.Account)
	if err != nil {
		return nil, output.Errorf(output.CodeConfigUnknownAccount, "%s", err.Error()).
			WithDetails("account", id.Account)
	}
	return acct, nil
}
