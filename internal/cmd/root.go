// Package cmd assembles the CLI from independently owned subcommands.
package cmd

import (
	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/cache"
	"github.com/zoomoid/kubectxmgr/internal/cmd/completion"
	"github.com/zoomoid/kubectxmgr/internal/cmd/currentcontext"
	"github.com/zoomoid/kubectxmgr/internal/cmd/discover"
	"github.com/zoomoid/kubectxmgr/internal/cmd/discovery"
	"github.com/zoomoid/kubectxmgr/internal/cmd/exec"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs"
	"github.com/zoomoid/kubectxmgr/internal/cmd/listcontexts"
	"github.com/zoomoid/kubectxmgr/internal/cmd/namespace"
	"github.com/zoomoid/kubectxmgr/internal/cmd/restore"
	"github.com/zoomoid/kubectxmgr/internal/cmd/setcontext"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shellinit"
	"github.com/zoomoid/kubectxmgr/internal/cmd/unsetcontext"
	"github.com/zoomoid/kubectxmgr/internal/cmd/version"
)

// New builds an independent command tree with shared persistent options.
// Constructing the tree performs no configuration reads or provider initialization.
func New() *cobra.Command {
	options := &shared.Options{}
	root := &cobra.Command{
		Use:           "switcher [context|.]",
		Short:         "Cached Kubernetes context switching",
		Version:       "dev",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Restore bypasses even configuration parsing, including for the "." shorthand.
			if len(args) == 1 && args[0] == "." {
				return restore.Run(cmd, options)
			}

			name := ""
			if len(args) > 0 {
				name = args[0]
			}

			return setcontext.Run(cmd, options, name)
		},
	}
	options.Bind(root)

	root.AddCommand(
		restore.New(options),
		setcontext.New(options),
		listcontexts.New(options),
		currentcontext.New(),
		unsetcontext.New(options),
		discover.New(options),
		discovery.New(options),
		namespace.New(options),
		exec.New(options),
		jobs.New(options),
		cache.New(options),
		shellinit.New(),
		completion.New(),
		version.New(),
	)

	return root
}
