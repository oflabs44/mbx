package main

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/oflabs44/mbx/internal/output"
	"github.com/oflabs44/mbx/internal/secret"
)

type GlobalFlags struct {
	Accounts []string
	Output   string
	Config   string
	Strict   bool
	Verbose  bool
	Debug    bool
	NoColor  bool
	Timeout  time.Duration
}

func (g *GlobalFlags) format() output.Format {
	if g.Output == "table" {
		return output.FormatTable
	}
	return output.FormatJSON
}

func newRootCmd(stdout, stderr io.Writer) (*cobra.Command, *GlobalFlags) {
	g := &GlobalFlags{}

	cmd := &cobra.Command{
		Use:           "mbx",
		Short:         "Agent-first email CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	f := cmd.PersistentFlags()
	f.StringSliceVarP(&g.Accounts, "account", "a", nil, "Account name(s). Required unless implicit in an mbx ID. Accepts repeated flag or comma list. The literal `all` fans out to every account in the config.")
	f.StringVarP(&g.Output, "output", "o", "json", "Output format: json | table")
	f.StringVarP(&g.Config, "config", "c", "", "Override config file path (default: ~/.config/mbx/config.toml)")
	f.BoolVar(&g.Strict, "strict", false, "Fanout: fail if any account in -a a,b fails")
	f.BoolVar(&g.Verbose, "verbose", false, "Verbose stderr logs")
	f.BoolVar(&g.Debug, "debug", false, "Debug-level stderr logs")
	f.BoolVar(&g.NoColor, "no-color", false, "Disable color in -o table output")
	f.DurationVar(&g.Timeout, "timeout", 0, "Overall command timeout (e.g. 30s, 2m). 0 = no deadline.")

	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &output.Failure{Code: output.CodeUsageInvalid, Message: err.Error()}
	})

	// Wire global debug switches to package-level loggers once flag parsing
	// completes. Runs before any subcommand's RunE.
	cmd.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if g.Debug {
			secret.Debug = func(format string, args ...any) {
				fmt.Fprintf(stderr, "secret: "+format+"\n", args...)
			}
		}
		return nil
	}

	cmd.AddCommand(
		newVersionCmd(g, stdout, stderr),
		newAccountCmd(g, stdout, stderr),
		newEnvelopeCmd(g, stdout, stderr),
		newMessageCmd(g, stdout, stderr),
		newFolderCmd(g, stdout, stderr),
		newAttachmentCmd(g, stdout, stderr),
		newCacheCmd(g, stdout, stderr),
		newCompletionCmd(stdout),
	)

	return cmd, g
}
