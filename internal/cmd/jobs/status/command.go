// Package status inspects the native discovery scheduler.
package status

import (
	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/options"
	"github.com/zoomoid/kubectxmgr/internal/jobs"
)

// New builds scheduler status inspection without loading provider configuration.
func New(options *options.Options) *cobra.Command {
	return &cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := options.Resolve(cmd, false)
			if err != nil {
				return err
			}

			data, err := jobs.Status(cmd.Context(), o, jobs.Run)
			if err != nil {
				return err
			}

			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
}
