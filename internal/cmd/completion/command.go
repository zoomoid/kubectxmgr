// Package completion emits Cobra completion for the assembled root command.
package completion

import (
	"errors"

	"github.com/spf13/cobra"
)

// New builds completion generation against the command's eventual root.
func New() *cobra.Command {
	return &cobra.Command{
		Use:  "completion <bash|zsh|fish>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			default:
				return errors.New("unsupported completion shell")
			}
		},
	}
}
