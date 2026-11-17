package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// cached serializes credential refreshes while leaving inventory discovery uncached.
type cached struct {
	Provider
	store config.Store
	disk  state.Disk
}

// Materialize reuses an owned unexpired kubeconfig or publishes a fresh one under
// a per-reference lock. Configuration changes create a distinct cache identity.
func (c cached) Materialize(ctx context.Context, r state.Reference) ([]byte, error) {
	disk := c.disk
	if c.store.Cache != nil {
		disk.Root = c.store.Cache.Config.Path
	}

	key := r.ID + c.store.Fingerprint()
	unlock, err := disk.Lock(ctx, "credential-"+key, true)
	if err != nil {
		return nil, err
	}
	defer unlock()

	path := disk.Path("credentials", key)
	cache, err := state.Read[state.Credentials](path)
	if err == nil {
		if cache.Owner == "kubectxmgr" && cache.Version == state.Version && time.Now().Before(cache.Expires) {
			return cache.Data, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	data, err := c.Provider.Materialize(ctx, r)
	if err != nil {
		return nil, err
	}

	err = state.Atomic(path, state.Credentials{Owner: "kubectxmgr", Key: key, Version: state.Version, Expires: time.Now().Add(time.Hour), Data: data})

	return data, err
}

// CredentialDirectory locates the cache whose owned entries cache clean may remove.
func CredentialDirectory(s config.Store, disk state.Disk) string {
	if s.Cache != nil {
		return filepath.Join(s.Cache.Config.Path, "credentials")
	}

	return filepath.Join(disk.Root, "credentials")
}
