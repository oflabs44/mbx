package main

import (
	"errors"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/account"
	"github.com/oflabs44/mbx/internal/account/auth"
	"github.com/oflabs44/mbx/internal/config"
	"github.com/oflabs44/mbx/internal/output"
	"github.com/oflabs44/mbx/internal/secret"
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
		newAccountAuthCmd(g, stdout, stderr),
		newAccountDoctorCmd(g, stdout, stderr),
		newAccountRemoveCmd(g, stdout, stderr),
		newAccountRenameCmd(g, stdout, stderr),
	)
	return cmd
}

type accountRenameResult struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Path  string `json:"path"`
	Alias string `json:"alias"`
}

func newAccountRenameCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:     "rename <old> <new>",
		Short:   "Rename an account; the old name is added as an alias so prior mbx IDs keep resolving",
		Example: `  mbx account rename personal personal-gmail`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]
			path, err := resolveConfigPath(g)
			if err != nil {
				return err
			}
			if err := account.RenameAccount(path, oldName, newName); err != nil {
				return mapRenameError(err, oldName, newName, path)
			}
			renameCacheRows(cmd.Context(), g, stderr, oldName, newName)
			return output.NewWriter(stdout, stderr, g.format()).Success(accountRenameResult{
				From:  oldName,
				To:    newName,
				Path:  path,
				Alias: oldName,
			}, nil)
		},
	}
}

func mapRenameError(err error, oldName, newName, path string) error {
	switch {
	case errors.Is(err, account.ErrAccountAbsent):
		return output.Errorf(output.CodeConfigUnknownAccount,
			"account %q not present in %s", oldName, path).
			WithDetails("account", oldName).
			WithDetails("path", path)
	case errors.Is(err, account.ErrRenameTargetExists):
		return output.Errorf(output.CodeConfigInvalid,
			"target name %q already present in %s", newName, path).
			WithDetails("account", newName).
			WithDetails("path", path)
	case errors.Is(err, account.ErrRenameNeedsManualAliasMerge):
		return output.Errorf(output.CodeConfigInvalid,
			"account %q already has an aliases list; merge by hand then re-run rename", oldName).
			WithDetails("account", oldName).
			WithDetails("path", path)
	}
	return output.Errorf(output.CodeConfigInvalid, "renaming account in %s: %s", path, err.Error())
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
		Example: `  mbx account add gmail-personal --type gmail
  mbx account add work --type imap`,
		Args: cobra.ExactArgs(1),
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

type accountAuthResult struct {
	Account   string   `json:"account"`
	Email     string   `json:"email"`
	Scopes    []string `json:"scopes,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

func newAccountAuthCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:     "auth <name>",
		Short:   "Run the OAuth flow and persist the refresh token via write_cmd",
		Example: `  mbx account auth gmail-personal`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			c, err := loadConfig(g)
			if err != nil {
				return err
			}
			cname, acct, err := account.Lookup(c, name)
			if err != nil {
				return output.Errorf(output.CodeConfigUnknownAccount, "%s", err.Error()).
					WithDetails("account", name)
			}
			name = cname

			authBlock := &acct.Backend.Auth
			if authBlock.Type != config.AuthOAuth2 {
				return output.Errorf(output.CodeUsageInvalid,
					"account %q uses %q auth, not oauth2; nothing to authorize", name, authBlock.Type).
					WithDetails("account", name)
			}
			if !authBlock.RefreshToken.HasWriteCmd() {
				return output.Errorf(output.CodeAuthMissingWriteCmd,
					"account %q has no backend.auth.refresh-token.write_cmd; cannot persist the rotating token", name).
					WithDetails("account", name)
			}

			// Preflight write_cmd before the browser dance. A broken write_cmd
			// is by far the most common reason this command fails, and failing
			// fast here means the user doesn't burn a consent grant on a
			// refresh token we can't store.
			if err := secret.Preflight(ctx, authBlock.RefreshToken); err != nil {
				return output.Errorf(output.CodeAuthRefreshFailed,
					"preflight of refresh-token.write_cmd failed: %s", err.Error()).
					WithDetails("account", name).
					WithDetails("hint", "verify your write_cmd executes cleanly: echo TEST | sh -c '<your-write-cmd>'")
			}

			oauthCfg, err := auth.Config(ctx, authBlock)
			if err != nil {
				return output.Errorf(output.CodeConfigInvalid, "building oauth2 config: %s", err.Error())
			}

			token, err := auth.Authorize(ctx, oauthCfg, auth.AuthorizeOpts{
				Scheme: authBlock.RedirectScheme,
				Host:   authBlock.RedirectHost,
				Port:   authBlock.RedirectPort,
				PKCE:   authBlock.PKCE,
			})
			if err != nil {
				return output.Errorf(output.CodeAuthRefreshFailed, "oauth flow failed: %s", err.Error())
			}
			if token.RefreshToken == "" {
				// Google issues refresh-tokens only on the first consent with
				// access_type=offline + prompt=consent. Surface as a hard error so
				// the user re-runs after fixing the consent URL or revoking the app
				// from their account.
				return output.Errorf(output.CodeAuthRefreshFailed,
					"provider returned no refresh token (ensure offline access + consent prompt are requested)").
					WithDetails("account", name)
			}

			if err := secret.Write(ctx, authBlock.RefreshToken, token.RefreshToken); err != nil {
				// Preflight has already verified the write_cmd round-trips, so a
				// failure here is genuinely unexpected — secret store outage,
				// network drop mid-call, etc. Re-running the command will re-do
				// the consent dance; that's acceptable for an edge case.
				return output.Errorf(output.CodeAuthRefreshFailed,
					"persisting refresh token via write_cmd: %s", err.Error()).
					WithDetails("account", name)
			}

			data := accountAuthResult{
				Account: name,
				Email:   acct.Email,
				Scopes:  oauthCfg.Scopes,
			}
			if !token.Expiry.IsZero() {
				data.ExpiresAt = token.Expiry.UTC().Format(time.RFC3339)
			}
			return output.NewWriter(stdout, stderr, g.format()).Success(data, nil)
		},
	}
}

type accountRemoveResult struct {
	Account string `json:"account"`
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
}

func newAccountRemoveCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Comment out the [accounts.<name>] block in the config file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path, err := resolveConfigPath(g)
			if err != nil {
				return err
			}

			alreadyCommented, err := account.RemoveAccount(path, name)
			if err != nil {
				if errors.Is(err, account.ErrAccountAbsent) {
					return output.Errorf(output.CodeConfigUnknownAccount,
						"account %q not present in %s", name, path).
						WithDetails("account", name).
						WithDetails("path", path)
				}
				return output.Errorf(output.CodeConfigInvalid,
					"removing %s: %s", path, err.Error())
			}

			return output.NewWriter(stdout, stderr, g.format()).Success(accountRemoveResult{
				Account: name,
				Path:    path,
				Removed: !alreadyCommented,
			}, nil)
		},
	}
}

func newAccountDoctorCmd(g *GlobalFlags, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:     "doctor <name>",
		Short:   "Probe an account: secrets resolve, auth refreshes, connectivity, capabilities",
		Example: `  mbx account doctor work`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]

			c, err := loadConfig(g)
			if err != nil {
				return err
			}
			report, err := account.Doctor(ctx, c, name)
			if err != nil {
				if errors.Is(err, config.ErrUnknownAccount) {
					return output.Errorf(output.CodeConfigUnknownAccount, "%s", err.Error()).
						WithDetails("account", name)
				}
				return output.Errorf(output.CodeGeneric, "%s", err.Error())
			}
			return output.NewWriter(stdout, stderr, g.format()).Success(report, nil)
		},
	}
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
