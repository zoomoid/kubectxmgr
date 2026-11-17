// Package namespace implements cached namespace selection for the active shell.
package namespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
	"github.com/zoomoid/kubectxmgr/internal/logging"
	"github.com/zoomoid/kubectxmgr/internal/namespacecache"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/tui"
	"golang.org/x/term"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// New builds namespace selection with the ns alias.
func New(options *shared.Options) *cobra.Command {
	return newCommand(options, func(cache namespacecache.Cache, cfg *clientcmdapi.Config) error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}

		return cache.Start(executable, cfg.CurrentContext)
	})
}

type refreshStarter func(namespacecache.Cache, *clientcmdapi.Config) error

// newCommand allows tests to control process creation without replacing API work.
func newCommand(options *shared.Options, start refreshStarter) *cobra.Command {
	var refreshContext string
	command := &cobra.Command{
		Use:     "namespace [name]",
		Aliases: []string{"ns"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			disk, err := options.Disk()
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("refresh-cache") {
				if len(args) != 0 {
					return errors.New("namespace cache refresh does not accept a namespace")
				}

				return refresh(cmd.Context(), disk, refreshContext)
			}

			cfg, err := shared.ActiveConfig()
			if err != nil {
				return err
			}

			cache, err := namespacecache.New(disk, cfg)
			if err != nil {
				return err
			}

			log := logging.New(cmd.ErrOrStderr(), options.Debug, options.JSONLogs)
			startRefresh := func() {
				if err := start(cache, cfg); err != nil {
					log.Warn("namespace cache refresh could not start", "error", err)
				}
			}

			name := ""
			if len(args) == 1 {
				// Explicit assignment must work without namespace-list permission.
				name = args[0]
			} else {
				name, err = pick(cmd, options, cfg, cache, startRefresh)
				if err != nil {
					return err
				}
			}

			if problems := validation.IsDNS1123Label(name); len(problems) > 0 {
				return fmt.Errorf("invalid namespace: %s", strings.Join(problems, "; "))
			}

			data, err := clientcmd.Write(*cfg)
			if err != nil {
				return err
			}

			data, name, err = kubeconfig.Select(data, cfg.CurrentContext, name)
			if err != nil {
				return err
			}

			// A child cannot export into its parent. Direct calls update the active
			// file; wrappers instead receive an isolated copy through their protocol.
			if options.ShellResult == "" {
				if err := kubeconfig.SetNamespace(cfg.CurrentContext, name); err != nil {
					return err
				}
			}

			if err := options.Save(cmd, disk, state.Selection{
				Context:    cfg.CurrentContext,
				Namespace:  name,
				Kubeconfig: data,
			}); err != nil {
				return err
			}

			if err := cache.RecordUse(cmd.Context(), name); err != nil {
				return err
			}

			if len(args) == 1 {
				startRefresh()
			}

			return nil
		},
	}
	command.Flags().StringVar(&refreshContext, "refresh-cache", "", "Refresh cached namespaces for a context")
	_ = command.Flags().MarkHidden("refresh-cache")

	return command
}

// pick serves stale inventories immediately; a cold cache needs one initial scan.
func pick(cmd *cobra.Command, options *shared.Options, cfg *clientcmdapi.Config, cache namespacecache.Cache, start func()) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("namespace selection needs a TTY; specify a namespace explicitly")
	}

	snapshot, err := cache.Load()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if snapshot.Refreshed.IsZero() {
		ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
		defer cancel()
		if err := cache.Refresh(ctx, cfg, true); err != nil {
			return "", err
		}

		snapshot, err = cache.Load()
		if err != nil {
			return "", err
		}
	} else {
		status, err := state.Read[state.Status](cache.Disk.Path("namespace-status", cache.Key))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if status.State == "failed" {
			logging.New(cmd.ErrOrStderr(), options.Debug, options.JSONLogs).
				Warn("last namespace refresh failed; using cached names", "error", status.Error)
		}

		start()
	}

	selected, err := tui.Pick(cmd.Context(), snapshot.References(), options.Regex)
	if err != nil {
		return "", err
	}

	return selected.Name, nil
}

// refresh runs in a detached CLI process and never changes namespace or selection.
func refresh(ctx context.Context, disk state.Disk, contextName string) error {
	cfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return err
	}

	cfg.CurrentContext = contextName
	cache, err := namespacecache.New(disk, cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	err = cache.Refresh(ctx, cfg, false)
	if errors.Is(err, state.ErrBusy) {
		return nil
	}

	return err
}
