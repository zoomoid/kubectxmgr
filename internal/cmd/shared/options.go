// Package shared provides persistent options and runtime wiring used by commands.
package shared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/discovery"
	"github.com/zoomoid/kubectxmgr/internal/execution"
	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
	"github.com/zoomoid/kubectxmgr/internal/logging"
	"github.com/zoomoid/kubectxmgr/internal/provider"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// selectionTemplate reports a human-readable selection without producing shell code.
var selectionTemplate = "context=%s namespace=%s\nKUBECONFIG=%s\n"

// Options contains persistent flag values shared by one command tree.
// Runtime dependencies are created lazily so local commands remain offline.
type Options struct {
	ConfigPath  string
	StatePath   string
	KubePath    string
	ShellResult string
	Machine     bool
	Verbose     bool
	Debug       bool
	JSONLogs    bool
	Regex       bool
}

// Bind registers the persistent flags on the root command.
func (o *Options) Bind(root *cobra.Command) {
	flags := root.PersistentFlags()
	flags.StringVar(&o.ConfigPath, "config-path", "~/.kube/switch-config.yaml", "SwitchConfig YAML file")
	flags.StringVar(&o.StatePath, "state-directory", "~/.kube/kubectxmgr", "Private application state directory")
	flags.StringVar(&o.KubePath, "kubeconfig-path", "", "Additional filesystem file or directory")
	flags.BoolVar(&o.Machine, "machine", false, "Print only the selected kubeconfig path")
	flags.BoolVarP(&o.Verbose, "verbose", "v", false, "Print selected context, namespace, and kubeconfig path")
	flags.StringVar(&o.ShellResult, "shell-result", "", "Write an environment action to a shell-owned response file")
	_ = flags.MarkHidden("shell-result")
	flags.BoolVar(&o.Debug, "debug", false, "Enable debug logging")
	flags.BoolVar(&o.JSONLogs, "json-logs", false, "Write structured JSON logs to stderr")
	flags.BoolVar(&o.Regex, "regex", false, "Use explicit RE2-compatible regular expression matching")
}

// Disk resolves the local state directory without reading cloud configuration.
func (o *Options) Disk() (state.Disk, error) {
	path, err := config.Expand(o.StatePath)
	if err != nil {
		return state.Disk{}, err
	}

	path, err = filepath.Abs(path)

	return state.Disk{Root: path}, err
}

// Engine loads configuration and wires discovery dependencies without scanning stores.
func (o *Options) Engine(cmd *cobra.Command) (*discovery.Engine, error) {
	disk, err := o.Disk()
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(o.ConfigPath, o.KubePath, disk.Root)
	if err != nil {
		return nil, err
	}

	log := logging.New(cmd.ErrOrStderr(), o.Debug, o.JSONLogs)
	for _, warning := range cfg.Warnings {
		log.Warn(warning)
	}

	// Generated caches must not be rediscovered by broad filesystem store globs.
	var excluded []string
	for _, store := range cfg.Stores {
		if store.Cache != nil {
			excluded = append(excluded, provider.CredentialDirectory(store, disk), filepath.Join(store.Cache.Config.Path, "locks"))
		}
	}

	return &discovery.Engine{
		Config:  cfg,
		Disk:    disk,
		Factory: provider.NewFactory(disk, log, excluded...),
		Log:     log,
	}, nil
}

// Report returns a selection through the requested verbose, machine, or shell channel.
// The shell response file is separate from stdout so child command output passes through.
func (o *Options) Report(cmd *cobra.Command, path string, selection state.Selection) error {
	if strings.ContainsAny(path, "\r\n\x00") {
		return errors.New("kubeconfig path cannot contain newline or NUL")
	}

	if o.ShellResult != "" {
		if err := os.WriteFile(o.ShellResult, []byte("set:"+path), 0600); err != nil {
			return err
		}
	} else if o.Machine {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), path)
		return err
	}

	if !o.Verbose {
		return nil
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), selectionTemplate, selection.Context, selection.Namespace, path)
	return err
}

// Save creates an isolated session kubeconfig, then publishes the global selection.
// A failed publication removes only the session file created by this operation.
func (o *Options) Save(cmd *cobra.Command, disk state.Disk, selection state.Selection) error {
	path, err := kubeconfig.Session(disk.Root, selection.Kubeconfig)
	if err != nil {
		return err
	}

	if err := disk.SaveSelection(cmd.Context(), selection); err != nil {
		_ = os.Remove(path)
		return err
	}

	return o.Report(cmd, path, selection)
}

// Materializer resolves a selected reference using its configured provider.
// Remote kubeconfigs are normalized to the inventory's canonical context name.
func Materializer(engine *discovery.Engine) execution.Materialize {
	return func(ctx context.Context, ref state.Reference) ([]byte, error) {
		for _, store := range engine.Config.Stores {
			if store.Key() != ref.Store {
				continue
			}

			p, err := engine.Factory(store)
			if err != nil {
				return nil, err
			}

			ctx, cancel := context.WithTimeout(ctx, engine.Config.Timeout())
			defer cancel()

			data, err := p.Materialize(ctx, ref)
			if err != nil {
				return nil, err
			}

			if store.Kind == "filesystem" {
				return data, nil
			}

			cfg, err := clientcmd.Load(data)
			if err != nil {
				return nil, err
			}

			if cfg.CurrentContext == "" || cfg.Contexts[cfg.CurrentContext] == nil {
				return nil, errors.New("provider kubeconfig has no valid current context")
			}

			current := cfg.Contexts[cfg.CurrentContext]
			cfg.Contexts = map[string]*clientcmdapi.Context{ref.Context: current}
			cfg.CurrentContext = ref.Context

			return clientcmd.Write(*cfg)
		}

		return nil, fmt.Errorf("store %s is no longer configured", ref.Store)
	}
}

// ActiveConfig loads the calling shell's kubeconfig and resolves its local paths.
// It deliberately does not consult the globally saved selection.
func ActiveConfig() (*clientcmdapi.Config, error) {
	cfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return nil, err
	}

	if cfg.CurrentContext == "" || cfg.Contexts[cfg.CurrentContext] == nil {
		return nil, errors.New("no active kubeconfig context")
	}

	if err := clientcmd.ResolveLocalPaths(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
