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

type prefetchProvider struct {
	fakeProvider
	materialized []string
	prefetchErr  error
}

func (p *prefetchProvider) Materialize(_ context.Context, ref state.Reference) ([]byte, error) {
	p.materialized = append(p.materialized, ref.ID)
	return []byte("private kubeconfig"), p.prefetchErr
}

func TestAKSPrefetchIsSeparateFromMetadataOnlyDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		prefetch, exhaustive bool
	}{
		{name: "metadata only", exhaustive: true},
		{name: "normal discovery", prefetch: true},
		{name: "exhaustive disables prefetch", prefetch: true, exhaustive: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Kind: "SwitchConfig", Version: "v1alpha1", Stores: []config.Store{{
				Kind: "azure", Config: config.Settings{SubscriptionID: "sub"},
			}}}
			require.NoError(t, cfg.Validate())
			store := cfg.Stores[0]
			p := &prefetchProvider{fakeProvider: fakeProvider{refs: []state.Reference{
				{ID: "one", Store: store.Key(), Metadata: map[string]string{"name": "one"}},
				{ID: "two", Store: store.Key(), Metadata: map[string]string{"name": "two"}},
			}}}
			disk := state.Disk{Root: t.TempDir()}
			e := &Engine{
				Config: cfg, Disk: disk, Log: logging.New(io.Discard, false, false),
				PrefetchCredentials: tt.prefetch,
				Factory: func(config.Store) (provider.Provider, error) {
					return p, nil
				},
			}
			_, err := e.Run(t.Context(), false, tt.exhaustive)
			require.NoError(t, err)
			if tt.prefetch && !tt.exhaustive {
				require.Equal(t, []string{"one", "two"}, p.materialized)
			} else {
				require.Empty(t, p.materialized)
			}

			snapshot, err := disk.Snapshot(store.Key(), store.Fingerprint())
			require.NoError(t, err)
			require.Equal(t, p.refs, snapshot.References)
			raw, err := os.ReadFile(disk.Path("inventory", store.Key()))
			require.NoError(t, err)
			require.NotContains(t, string(raw), "private kubeconfig")
		})
	}
}

func TestAKSPrefetchFailureRetainsCompleteInventory(t *testing.T) {
	cfg := &config.Config{Kind: "SwitchConfig", Version: "v1alpha1", Stores: []config.Store{{
		Kind: "azure", Config: config.Settings{SubscriptionID: "sub"},
	}}}
	require.NoError(t, cfg.Validate())
	store := cfg.Stores[0]
	disk := state.Disk{Root: t.TempDir()}
	path := disk.Path("inventory", store.Key())
	require.NoError(t, state.Atomic(path, state.Snapshot{
		Version: state.Version, Fingerprint: store.Fingerprint(), Complete: true,
		Discovered: time.Now(), References: []state.Reference{{ID: "old", Store: store.Key()}},
	}))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	p := &prefetchProvider{
		fakeProvider: fakeProvider{refs: []state.Reference{{ID: "new", Store: store.Key()}}},
		prefetchErr:  errors.New("user kubeconfig denied"),
	}
	e := &Engine{
		Config: cfg, Disk: disk, Log: logging.New(io.Discard, false, false),
		PrefetchCredentials: true,
		Factory: func(config.Store) (provider.Provider, error) {
			return p, nil
		},
	}
	_, err = e.Run(t.Context(), false, false)
	require.ErrorContains(t, err, "user kubeconfig denied")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	status, err := state.Read[state.Status](disk.Path("status", store.Key()))
	require.NoError(t, err)
	require.Equal(t, "failed", status.State)
}
