package main

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/account"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/output"
)

func newAccountCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Configure and inspect accounts",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newAccountListCmd(g, stdout, stderr),
		newAccountAddCmd(g, stdout, stderr),
	)
	return cmd
}

func newAccountListCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadConfig(g)
			if err != nil {
				return err
			}
			return output.NewWriter(stdout, stderr, g.format()).Success(account.List(c), nil)
		},
	}
}

type accountAddResult struct {
	Account string `json:"account"`
	Type    string `json:"type"`
	Path    string `json:"path"`
}

func newAccountAddCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	var typeFlag string
	c := &cobra.Command{
		Use:   "add <name>",
		Short: "Append a commented [accounts.<name>] template to the config file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			tmpl, err := templateFor(typeFlag, name)
			if err != nil {
				return err
			}

			path, err := resolveConfigPath(g)
			if err != nil {
				return err
			}

			if err := account.AddTemplate(path, name, tmpl); err != nil {
				return mapAddError(err, name, path)
			}

			return output.NewWriter(stdout, stderr, g.format()).Success(accountAddResult{
				Account: name,
				Type:    typeFlag,
				Path:    path,
			}, nil)
		},
	}
	c.Flags().StringVar(&typeFlag, "type", "gmail", "Account type: gmail | imap")
	return c
}

func templateFor(t, name string) (string, error) {
	switch t {
	case "gmail":
		return account.GmailTemplate(name), nil
	case "imap":
		return account.IMAPTemplate(name), nil
	default:
		return "", output.Errorf(output.CodeUsageInvalid, "--type must be gmail or imap (got %q)", t)
	}
}

func resolveConfigPath(g *GlobalFlags) (string, error) {
	if g.Config != "" {
		return g.Config, nil
	}
	p, err := config.DefaultPath()
	if err != nil {
		return "", output.Errorf(output.CodeConfigInvalid, "resolving config path: %s", err.Error())
	}
	return p, nil
}

func loadConfig(g *GlobalFlags) (*config.Config, error) {
	path, err := resolveConfigPath(g)
	if err != nil {
		return nil, err
	}
	c, err := config.Load(path)
	if err != nil {
		return nil, mapConfigError(err)
	}
	return c, nil
}

func mapConfigError(err error) error {
	if errors.Is(err, config.ErrUnknownAccount) {
		return output.Errorf(output.CodeConfigUnknownAccount, "%s", err.Error())
	}
	return output.Errorf(output.CodeConfigInvalid, "%s", err.Error())
}

func mapAddError(err error, name, path string) error {
	if errors.Is(err, account.ErrAccountExists) {
		return output.Errorf(output.CodeConfigInvalid,
			"account %q already present in %s", name, path).
			WithDetails("account", name).
			WithDetails("path", path)
	}
	// Filesystem failures (MkdirAll, tempfile write, rename) currently collapse
	// to config.invalid because the code taxonomy has no I/O bucket. The
	// wrapped message preserves the underlying cause; add a dedicated code
	// when the next disk-touching verb (account auth) lands.
	return output.Errorf(output.CodeConfigInvalid, "writing %s: %s", path, err.Error())
}
