// Package restore implements local-only restoration of the saved selection.
package restore

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
)

// New builds the explicit restore subcommand.
func New(options *shared.Options) *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Restore last context and namespace without network access",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Run(cmd, options)
		},
	}
}

// Run restores the last context and namespace into an independent session file.
// This path must never construct a discovery engine or wait on discovery locks.
func Run(cmd *cobra.Command, options *shared.Options) error {
	disk, err := options.Disk()
	if err != nil {
		return err
	}

	last, err := disk.Last()
	if err != nil {
		return err
	}

	data, namespace, err := kubeconfig.Select(last.Kubeconfig, last.Context, last.Namespace)
	if err != nil {
		return fmt.Errorf("invalid local saved kubeconfig: %w", err)
	}

	last.Namespace = namespace
	path, err := kubeconfig.Session(disk.Root, data)
	if err != nil {
		return err
	}

	return options.Report(cmd, path, last)
}
