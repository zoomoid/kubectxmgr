// Package clean removes only application-owned credential-cache entries.
package clean

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/provider"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// New builds credential cleanup without touching saved selections or session files.
func New(options *shared.Options) *cobra.Command {
	return &cobra.Command{
		Use:  "clean",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			engine, err := options.Engine(cmd)
			if err != nil {
				return err
			}

			dirs := map[string]bool{filepath.Join(engine.Disk.Root, "credentials"): true}
			for _, store := range engine.Config.Stores {
				dirs[provider.CredentialDirectory(store, engine.Disk)] = true
			}

			for dir := range dirs {
				if err := cleanDirectory(cmd.Context(), dir); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

// cleanDirectory validates cache ownership and filename identity before removal.
func cleanDirectory(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		credentials, err := state.Read[state.Credentials](path)
		if err != nil {
			return err
		}

		if credentials.Version != state.Version || credentials.Owner != "kubectxmgr" || credentials.Key == "" || state.Hash(credentials.Key)+".yaml" != entry.Name() {
			return fmt.Errorf("refusing unowned cache file %s", path)
		}

		if err := cleanCredential(ctx, state.Disk{Root: filepath.Dir(dir)}, path, credentials.Key); err != nil {
			return err
		}
	}

	return nil
}

// cleanCredential removes one validated entry under its credential-writer lock.
func cleanCredential(ctx context.Context, disk state.Disk, path, key string) error {
	// Serialize removal with credential generation, not with inventory discovery.
	unlock, err := disk.Lock(ctx, "credential-"+key, true)
	if err != nil {
		return err
	}
	defer unlock()

	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}
