package discovery

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/logging"
	"github.com/zoomoid/kubectxmgr/internal/provider"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

type fakeProvider struct {
	refs []state.Reference
	err  error
}

func (f fakeProvider) Discover(context.Context) ([]state.Reference, error) { return f.refs, f.err }
func (f fakeProvider) Materialize(context.Context, state.Reference) ([]byte, error) {
	panic("discovery must not materialize")
}

func TestIndependentExhaustivePublication(t *testing.T) {
	optional := false
	cfg := &config.Config{Kind: "SwitchConfig", Version: "v1alpha1", Stores: []config.Store{{Kind: "eks", ID: "good"}, {Kind: "eks", ID: "bad", Required: &optional}}}
	require.NoError(t, cfg.Validate())
	disk := state.Disk{Root: t.TempDir()}
	bad := cfg.Stores[1]
	path := disk.Path("inventory", bad.Key())
	require.NoError(t, state.Atomic(path, state.Snapshot{Version: state.Version, Fingerprint: bad.Fingerprint(), Complete: true, Discovered: time.Now(), References: []state.Reference{{ID: "old", Store: bad.Key()}}}))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	e := &Engine{Config: cfg, Disk: disk, Log: logging.New(io.Discard, false, false), Factory: func(s config.Store) (provider.Provider, error) {
		if s.ID == "bad" {
			return fakeProvider{refs: []state.Reference{{ID: "partial", Store: s.Key()}}, err: errors.New("page 2 failed")}, nil
		}
		return fakeProvider{}, nil
	}}
	refs, err := e.Run(t.Context(), false, false)
	require.NoError(t, err)
	require.Empty(t, refs)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	good, err := disk.Snapshot(cfg.Stores[0].Key(), cfg.Stores[0].Fingerprint())
	require.NoError(t, err)
	require.True(t, good.Complete)
	require.Empty(t, good.References)
	_, err = e.Run(t.Context(), false, true)
	require.ErrorContains(t, err, "page 2 failed")
}

func TestDueAndCancelledDiscovery(t *testing.T) {
	cfg := &config.Config{Kind: "SwitchConfig", Version: "v1alpha1", Stores: []config.Store{{Kind: "eks"}}}
	require.NoError(t, cfg.Validate())
	disk := state.Disk{Root: t.TempDir()}
	calls := 0
	e := &Engine{Config: cfg, Disk: disk, Log: logging.New(io.Discard, false, false), Factory: func(config.Store) (provider.Provider, error) { calls++; return fakeProvider{}, nil }}
	_, err := e.Run(t.Context(), true, false)
	require.NoError(t, err)
	_, err = e.Run(t.Context(), true, false)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = e.Run(ctx, false, true)
	require.ErrorIs(t, err, context.Canceled)
}
