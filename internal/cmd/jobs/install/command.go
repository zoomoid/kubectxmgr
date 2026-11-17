// Package install writes and optionally activates per-user discovery jobs.
package install

import (
	"errors"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/options"
	"github.com/zoomoid/kubectxmgr/internal/jobs"
)

// New builds job installation with explicit overwrite and activation controls.
func New(options *options.Options) *cobra.Command {
	var noEnable, force bool
	cmd := &cobra.Command{
		Use:  "install",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := options.Resolve(cmd, true)
			if err != nil {
				return err
			}

			if o.Platform != runtime.GOOS {
				return errors.New("cannot install a job for another platform; use jobs template")
			}

			return jobs.Install(cmd.Context(), o, !noEnable, force, jobs.Run)
		},
	}
	cmd.Flags().BoolVar(&noEnable, "no-enable", false, "Write files without enabling the job")
	cmd.Flags().BoolVar(&force, "force", false, "Replace unrelated files at exact job paths")

	return cmd
}
