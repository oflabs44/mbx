package main

import (
	"io"

	"github.com/spf13/cobra"
)

// newCompletionCmd surfaces Cobra's built-in shell completion generator.
// Per-shell instructions live in the subcommand Long strings so `mbx
// completion bash --help` is a self-contained recipe.
//
// Dynamic completion (account names from config, folder names from a
// live backend) is intentionally out of scope for v0.1 — invoking the
// CLI from a completion hook would burn an API roundtrip on every Tab.
func newCompletionCmd(stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `Generate a shell completion script for mbx.

The script is printed to stdout. Pipe it into your shell's completion
directory or source it from your shell rc file.

Examples:
  # bash (system-wide on Linux)
  mbx completion bash | sudo tee /etc/bash_completion.d/mbx >/dev/null

  # bash (one-shot per session)
  source <(mbx completion bash)

  # zsh
  mbx completion zsh > "${fpath[1]}/_mbx"

  # fish
  mbx completion fish > ~/.config/fish/completions/mbx.fish`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(stdout)
			}
			return nil // unreachable per ValidArgs
		},
	}
	return cmd
}
