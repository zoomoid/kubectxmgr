// Package uninstall removes owned per-user discovery jobs.
package uninstall

import (
	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/options"
	"github.com/zoomoid/kubectxmgr/internal/jobs"
)

// New builds job removal without requiring valid provider configuration.
func New(options *options.Options) *cobra.Command {
	return &cobra.Command{
		Use:  "uninstall",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := options.Resolve(cmd, false)
			if err != nil {
				return err
			}

			return jobs.Uninstall(cmd.Context(), o, jobs.Run)
		},
	}
}
