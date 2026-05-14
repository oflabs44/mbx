package main

import (
	"context"
	"io"

	"github.com/spf13/cobra"

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
