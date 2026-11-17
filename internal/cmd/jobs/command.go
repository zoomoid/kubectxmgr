// Package jobs assembles the per-user discovery scheduler commands.
package jobs

import (
	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/install"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/options"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/status"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/template"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/uninstall"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
)

// New assembles scheduler subcommands with one shared set of job flags.
func New(common *shared.Options) *cobra.Command {
	o := &options.Options{Common: common}
	cmd := &cobra.Command{Use: "jobs"}
	o.Bind(cmd)
	cmd.AddCommand(template.New(o), install.New(o), uninstall.New(o), status.New(o))

	return cmd
}
