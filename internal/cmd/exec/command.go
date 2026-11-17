// Package exec implements fresh-inventory command execution and dry-run planning.
package exec

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/execution"
)

// New builds execution and dry-run commands using the same target planner.
func New(options *shared.Options) *cobra.Command {
	var dryRun, jsonOutput bool
	cmd := &cobra.Command{
		Use:  "exec <pattern> -- <command> [args...]",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.ArgsLenAtDash() != 1 {
				return errors.New("provide exactly one pattern followed by -- and a command")
			}

			engine, err := options.Engine(cmd)
			if err != nil {
				return err
			}

			mode := "glob"
			if options.Regex {
				mode = "regex"
			}

			// Argument validation precedes discovery; execution always requires fresh results.
			if err := execution.Validate(args[0], mode, engine.Config.ExecShell, args[1:]); err != nil {
				return err
			}

			refs, err := engine.Run(cmd.Context(), false, true)
			if err != nil {
				return fmt.Errorf("execution requires fresh complete discovery of every store: %w", err)
			}

			targets, err := execution.Plan(refs, args[0], mode, engine.Config.ExecShell, args[1:])
			if err != nil {
				return err
			}

			if dryRun {
				return execution.Print(cmd.OutOrStdout(), targets, jsonOutput)
			}

			if jsonOutput {
				return errors.New("--json is only supported with --dry-run")
			}

			return execution.Run(cmd.Context(), engine.Disk.Root, targets, shared.Materializer(engine), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Discover and print exact target commands without materializing credentials")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print a machine-readable dry-run plan")

	return cmd
}
