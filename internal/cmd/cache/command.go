// Package cache assembles credential-cache maintenance commands.
package cache

import (
	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/cache/clean"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
)

// New builds the cache maintenance command tree.
func New(options *shared.Options) *cobra.Command {
	cmd := &cobra.Command{Use: "cache"}
	cmd.AddCommand(clean.New(options))

	return cmd
}
