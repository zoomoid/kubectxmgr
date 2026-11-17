// Package listcontexts exposes cached contexts without triggering discovery.
package listcontexts

import (
	"encoding/json"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/search"
)

// New builds the cached inventory listing command.
func New(options *shared.Options) *cobra.Command {
	return &cobra.Command{
		Use:  "list-contexts [pattern]",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := options.Engine(cmd)
			if err != nil {
				return err
			}

			refs, _ := engine.Cached()
			if len(args) == 1 {
				mode := "glob"
				if options.Regex {
					mode = "regex"
				}

				refs, err = search.Match(refs, args[0], mode)
				if err != nil {
					return err
				}
			}

			return json.NewEncoder(cmd.OutOrStdout()).Encode(refs)
		},
	}
}
