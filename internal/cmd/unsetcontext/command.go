// Package unsetcontext requests shell-local removal of KUBECONFIG.
package unsetcontext

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
)

// New builds the unset-context command used by the shell response protocol.
func New(options *shared.Options) *cobra.Command {
	return &cobra.Command{
		Use:  "unset-context",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if options.ShellResult != "" {
				return os.WriteFile(options.ShellResult, []byte("unset"), 0600)
			}

			return errors.New("a child process cannot unset the parent environment; use the switch shell function")
		},
	}
}
