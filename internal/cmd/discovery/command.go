// Package discovery groups discovery inspection commands.
package discovery

import (
	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/discovery/status"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
)

// New assembles the discovery inspection command tree.
func New(options *shared.Options) *cobra.Command {
	cmd := &cobra.Command{Use: "discovery"}
	cmd.AddCommand(status.New(options))

	return cmd
}
