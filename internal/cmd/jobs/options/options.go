// Package options resolves shared scheduler flags for the jobs subcommands.
package options

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/jobs"
)

// Options contains job flags and the enclosing command tree's persistent options.
type Options struct {
	Common     *shared.Options
	Platform   string
	Executable string
}

// Bind registers scheduler-specific flags inherited by the jobs subcommands.
func (o *Options) Bind(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&o.Platform, "platform", runtime.GOOS, "Job platform: linux or darwin")
	cmd.PersistentFlags().StringVar(&o.Executable, "executable", "", "Absolute installed switcher executable path")
}

// Resolve gathers scheduler paths and the non-secret environment allowlist.
// includeStores is false for status/removal so broken configuration cannot prevent cleanup.
func (o *Options) Resolve(cmd *cobra.Command, includeStores bool) (jobs.Options, error) {
	disk, err := o.Common.Disk()
	if err != nil {
		return jobs.Options{}, err
	}

	configPath, err := config.Expand(o.Common.ConfigPath)
	if err != nil {
		return jobs.Options{}, err
	}

	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return jobs.Options{}, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return jobs.Options{}, err
	}

	path := o.Executable
	if path == "" {
		path, err = os.Executable()
		if err != nil {
			return jobs.Options{}, err
		}
	}

	var stores []config.Store
	if includeStores {
		engine, err := o.Common.Engine(cmd)
		if err != nil {
			return jobs.Options{}, err
		}

		stores = engine.Config.Stores
	}

	interval := config.DefaultInterval
	var envPaths []string
	extraPath := ""
	for _, store := range stores {
		interval = min(interval, store.Interval)
		if store.Kind == "filesystem" && store.ID == "environment" {
			envPaths = append(envPaths, store.Paths...)
		}

		if o.Common.KubePath != "" && store.Kind == "filesystem" && store.ID == "flag" {
			extraPath = store.Paths[0]
		}
	}

	environment := map[string]string{}
	for _, key := range jobs.EnvironmentKeys {
		if value, ok := os.LookupEnv(key); ok {
			environment[key] = value
		}
	}

	return jobs.Options{
		Platform:    o.Platform,
		Home:        home,
		UID:         os.Getuid(),
		Executable:  path,
		Config:      configPath,
		State:       disk.Root,
		Path:        os.Getenv("PATH"),
		Kubeconfig:  strings.Join(envPaths, string(os.PathListSeparator)),
		ExtraPath:   extraPath,
		Interval:    interval,
		Environment: environment,
	}, nil
}
