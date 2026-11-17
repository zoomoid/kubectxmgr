// Package version reports executable and Go runtime versions.
package version

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// New builds version output using the assembled root command's version.
func New() *cobra.Command {
	return &cobra.Command{
		Use:  "version",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "switcher %s (%s %s/%s)\n", cmd.Root().Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return err
		},
	}
}
