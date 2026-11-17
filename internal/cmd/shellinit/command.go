// Package shellinit emits shell integration without editing startup files.
package shellinit

import (
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/shell"
)

// New builds the shell integration generation command.
func New() *cobra.Command {
	return &cobra.Command{
		Use:  "init <bash|zsh|fish>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			executable, err := os.Executable()
			if err != nil {
				return err
			}

			text, err := shell.Init(args[0], executable)
			if err != nil {
				return err
			}

			_, err = io.WriteString(cmd.OutOrStdout(), text)
			return err
		},
	}
}
