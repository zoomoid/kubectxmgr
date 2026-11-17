// Package currentcontext reports the calling shell's active context.
package currentcontext

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
)

// New builds the current-context inspection command.
func New() *cobra.Command {
	return &cobra.Command{
		Use:  "current-context",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := shared.ActiveConfig()
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), cfg.CurrentContext)
			return err
		},
	}
}
