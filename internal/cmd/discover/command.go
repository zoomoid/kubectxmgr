// Package discover runs explicit or scheduled store discovery.
package discover

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
)

// New builds the discovery command and its scheduled-refresh flag.
func New(options *shared.Options) *cobra.Command {
	var due bool
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Discover all configured scopes and publish complete store inventories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			engine, err := options.Engine(cmd)
			if err != nil {
				return err
			}

			refs, err := engine.Run(cmd.Context(), due, false)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Discovery completed; %d contexts from scanned stores. Check discovery status for optional failures/skips.\n", len(refs))
			return err
		},
	}
	cmd.Flags().BoolVar(&due, "due", false, "Scan only stores whose inventory is missing or due; skip busy stores")

	return cmd
}
