package main

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/folder"
	"github.com/oflabs44/mbx/internal/output"
)

func newFolderCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "folder",
		Short: "Manage folders (Gmail labels surfaced as folders)",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newFolderListCmd(g, stdout, stderr),
		newFolderAddCmd(g, stdout, stderr),
		newFolderDeleteCmd(g, stdout, stderr),
		newFolderExpungeCmd(g, stdout, stderr),
		newFolderPurgeCmd(g, stdout, stderr),
	)
	return cmd
}

func newFolderListCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List folders for an account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runFolderList(cmd.Context(), g, stdout, stderr)
		},
	}
}

func runFolderList(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer) error {
	acctName, acct, err := requireSingleAccount(g)
	if err != nil {
		return err
	}
	backend, err := newBackend(ctx, acctName, acct)
	if err != nil {
		return err
	}
	defer closeBackend(backend)
	folders, err := backend.ListFolders(ctx)
	if err != nil {
		return err
	}
	return output.NewWriter(stdout, stderr, g.format()).Success(folders, nil)
}

// folderMutateResult is the JSON shape every folder write-path verb
// emits on success. Name is the folder operated on; Op is the verb so
// callers can branch off a single shared shape.
type folderMutateResult struct {
	Name string `json:"name"`
	Op   string `json:"op"`
}

func newFolderAddCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name>",
		Short: "Create a new folder (Gmail: user label)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFolderMutation(cmd.Context(), g, stdout, stderr, "add", args[0], func(ctx context.Context, b backend, acctName string) error {
				adder, ok := b.(folder.Adder)
				if !ok {
					return unsupportedFolderErr(acctName, "add")
				}
				return adder.AddFolder(ctx, args[0])
			})
		},
	}
}

func newFolderDeleteCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a folder. Non-empty IMAP mailboxes refuse without --force.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFolderMutation(cmd.Context(), g, stdout, stderr, "delete", args[0], func(ctx context.Context, b backend, acctName string) error {
				deleter, ok := b.(folder.Deleter)
				if !ok {
					return unsupportedFolderErr(acctName, "delete")
				}
				return deleter.DeleteFolder(ctx, args[0], force)
			})
		},
	}
	c.Flags().BoolVar(&force, "force", false, "On IMAP, purge messages first if the folder is non-empty. No-op on Gmail.")
	return c
}

func newFolderExpungeCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "expunge <name>",
		Short: "Permanently remove messages already flagged \\Deleted (IMAP-only; no-op on Gmail).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFolderMutation(cmd.Context(), g, stdout, stderr, "expunge", args[0], func(ctx context.Context, b backend, acctName string) error {
				expunger, ok := b.(folder.Expunger)
				if !ok {
					return unsupportedFolderErr(acctName, "expunge")
				}
				return expunger.ExpungeFolder(ctx, args[0])
			})
		},
	}
}

func newFolderPurgeCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "purge <name>",
		Short: "Delete every message in the folder. Irreversible; requires --yes.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return output.Errorf(output.CodeInputMissingFlag,
					"purge is destructive and irreversible; pass --yes to confirm")
			}
			return runFolderMutation(cmd.Context(), g, stdout, stderr, "purge", args[0], func(ctx context.Context, b backend, acctName string) error {
				purger, ok := b.(folder.Purger)
				if !ok {
					return unsupportedFolderErr(acctName, "purge")
				}
				return purger.PurgeFolder(ctx, args[0])
			})
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "Required confirmation for this destructive verb.")
	return c
}

// runFolderMutation is the shared handler body for add/delete/expunge/
// purge: resolve account, open backend, run the verb closure, emit the
// success envelope. The verb closure decides which capability interface
// to assert and what call to make — handlers stay tiny.
//
// The closure receives the resolved account name (not just the raw -a
// flag) so error messages name the actual backend the verb ran against.
func runFolderMutation(ctx context.Context, g *GlobalFlags, stdout, stderr io.Writer, op, name string, run func(context.Context, backend, string) error) error {
	acctName, acct, err := requireSingleAccount(g)
	if err != nil {
		return err
	}
	b, err := newBackend(ctx, acctName, acct)
	if err != nil {
		return err
	}
	defer closeBackend(b)
	if err := run(ctx, b, acctName); err != nil {
		return err
	}
	data := folderMutateResult{Name: name, Op: op}
	meta := envelopeListMeta{AccountsQueried: []string{acctName}}
	return output.NewWriter(stdout, stderr, g.format()).Success(data, meta)
}

// unsupportedFolderErr surfaces the standard provider.unsupported code
// when a backend doesn't satisfy the capability interface the verb
// needs. Wraps the verb name + resolved account name for actionable
// output.
func unsupportedFolderErr(acctName, verb string) error {
	return output.Errorf(output.CodeProviderUnsupported,
		"folder %s is not supported for account %q", verb, acctName)
}
