// Package setcontext implements exact and interactive context selection.
package setcontext

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
	"github.com/zoomoid/kubectxmgr/internal/search"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/tui"
	"golang.org/x/term"
)

// New builds the noninteractive set-context subcommand.
func New(options *shared.Options) *cobra.Command {
	return &cobra.Command{
		Use:  "set-context <context>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(cmd, options, args[0])
		},
	}
}

// Run selects an exact context, or opens the picker when name is empty.
// Only a first interactive selection may discover stores without an explicit request.
func Run(cmd *cobra.Command, options *shared.Options, name string) error {
	if name == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("interactive selection needs a TTY; run discover and set-context explicitly")
	}

	engine, err := options.Engine(cmd)
	if err != nil {
		return err
	}

	refs, valid := engine.Cached()
	if valid == 0 {
		if name != "" {
			return errors.New("no usable inventory; run switcher discover or interactive switcher first")
		}

		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "No inventory exists; performing initial discovery."); err != nil {
			return err
		}

		_, scanErr := engine.Run(cmd.Context(), false, false)
		refs, valid = engine.Cached()
		if scanErr != nil && valid == 0 {
			return scanErr
		}
	}

	if len(refs) == 0 {
		return errors.New("no contexts in available inventories; run switcher discover")
	}

	var ref state.Reference
	if name == "" {
		ref, err = tui.Pick(cmd.Context(), refs, options.Regex)
	} else {
		ref, err = search.Exact(refs, name)
	}
	if err != nil {
		return err
	}

	data, err := shared.Materializer(engine)(cmd.Context(), ref)
	if err != nil {
		return err
	}

	data, namespace, err := kubeconfig.Select(data, ref.Context, "")
	if err != nil {
		return err
	}

	return options.Save(cmd, engine.Disk, state.Selection{
		Context:    ref.Context,
		Namespace:  namespace,
		Kubeconfig: data,
	})
}
