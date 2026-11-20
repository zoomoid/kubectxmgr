// Package discovery coordinates bounded store scans and complete snapshot publication.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/provider"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// Engine connects validated configuration, providers, and a shared state directory.
type Engine struct {
	Config  *config.Config
	Disk    state.Disk
	Factory provider.Factory
	Log     *slog.Logger
	// PrefetchCredentials warms private AKS kubeconfigs during normal discovery.
	// Execution and dry-run leave this disabled to keep discovery metadata-only.
	PrefetchCredentials bool
}

// Result records one store scan, including refreshes skipped by scheduled discovery.
type Result struct {
	Store      config.Store
	References []state.Reference
	Error      error
	Skipped    bool
}

// scan holds the store lock across freshness checking, discovery, and publication.
// Status is separate from inventory so a failed scan cannot invalidate old readers.
func (e *Engine) scan(ctx context.Context, s config.Store, due, prefetch bool) (refs []state.Reference, skipped bool, err error) {
	unlock, err := e.Disk.Lock(ctx, "discover-"+s.Key(), !due)
	if errors.Is(err, state.ErrBusy) && due {
		e.Log.Info("discovery skipped: store busy", "store", s.Key())
		return nil, true, nil
	}

	if err != nil {
		return nil, false, err
	}
	defer unlock()

	if due {
		snapshot, readErr := e.Disk.Snapshot(s.Key(), s.Fingerprint())
		if readErr == nil && time.Since(snapshot.Discovered) < s.Interval {
			return snapshot.References, true, nil
		}

		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			e.Log.Warn("inventory needs refresh", "store", s.Key(), "error", readErr)
		}
	}

	status := state.Status{Version: state.Version, Attempt: time.Now().UTC(), State: "running"}
	statusPath := e.Disk.Path("status", s.Key())
	if err := state.Atomic(statusPath, status); err != nil {
		return nil, false, err
	}

	e.Log.Info("discovering store", "store", s.Key())
	defer func() {
		if err != nil {
			status.State = "failed"
			status.Error = err.Error()
		} else {
			status.State = "complete"
		}

		err = errors.Join(err, state.Atomic(statusPath, status))
	}()

	runCtx, cancel := context.WithTimeout(ctx, e.Config.Timeout())
	defer cancel()

	p, err := e.Factory(s)
	if err != nil {
		return nil, false, err
	}

	refs, err = p.Discover(runCtx)
	if err != nil {
		return nil, false, err
	}

	if err := runCtx.Err(); err != nil {
		return nil, false, err
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	seen := map[string]bool{}
	for _, r := range refs {
		if r.ID == "" || r.Store != s.Key() || seen[r.ID] {
			return nil, false, errors.New("provider returned invalid/duplicate cluster identity")
		}

		seen[r.ID] = true
	}

	if prefetch && s.Kind == "azure" {
		for _, ref := range refs {
			if _, err := p.Materialize(runCtx, ref); err != nil {
				return nil, false, fmt.Errorf("prefetch AKS user kubeconfig for %s: %w", ref.ID, err)
			}
		}
	}

	if err := runCtx.Err(); err != nil {
		return nil, false, err
	}

	// Only a successful exhaustive result reaches the inventory replacement.
	err = state.Atomic(e.Disk.Path("inventory", s.Key()), state.Snapshot{
		Version: state.Version, Fingerprint: s.Fingerprint(), Complete: true, Discovered: time.Now().UTC(), References: refs,
	})
	if err == nil {
		e.Log.Info("published store inventory", "store", s.Key(), "contexts", len(refs))
	}

	return refs, false, err
}

// Run scans stores with bounded concurrency and publishes successful stores
// independently. due skips fresh or busy stores. exhaustive makes even optional
// store failures fatal and disables credential prefetch for execution and dry-run.
func (e *Engine) Run(ctx context.Context, due, exhaustive bool) ([]state.Reference, error) {
	if len(e.Config.Stores) == 0 {
		return nil, errors.New("no stores configured; configure SwitchConfig or a kubeconfig path")
	}

	jobs := make(chan config.Store)
	results := make(chan Result, len(e.Config.Stores))
	var wg sync.WaitGroup
	for range min(e.Config.Discovery.Concurrency, len(e.Config.Stores)) {
		wg.Go(func() {
			for s := range jobs {
				refs, skipped, err := e.scan(ctx, s, due, e.PrefetchCredentials && !exhaustive)
				results <- Result{Store: s, References: refs, Error: err, Skipped: skipped}
			}
		})
	}

	go func() {
		defer close(jobs)
		for _, s := range e.Config.Stores {
			jobs <- s
		}
	}()

	wg.Wait()
	close(results)

	var refs []state.Reference
	var errs []error
	for result := range results {
		if result.Error != nil {
			e.Log.Warn("store discovery failed; retaining previous inventory", "store", result.Store.Key(), "error", result.Error)
			if exhaustive || result.Store.IsRequired() {
				errs = append(errs, fmt.Errorf("%s: %w", result.Store.Key(), result.Error))
			}
		} else {
			refs = append(refs, result.References...)
		}
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })

	return refs, errors.Join(errs...)
}

// Cached reads matching complete inventories without provider calls or writer
// locks. The count includes valid empty snapshots; stale results remain usable
// and produce warnings alongside failed or missing stores.
func (e *Engine) Cached() ([]state.Reference, int) {
	var refs []state.Reference
	valid := 0
	for _, s := range e.Config.Stores {
		snapshot, err := e.Disk.Snapshot(s.Key(), s.Fingerprint())
		if err != nil {
			e.Log.Warn("store inventory unavailable; run switcher discover", "store", s.Key(), "error", err)
			continue
		}

		valid++
		if time.Since(snapshot.Discovered) >= s.Interval {
			e.Log.Warn("store inventory is stale", "store", s.Key(), "discoveredAt", snapshot.Discovered)
		}

		status, err := state.Read[state.Status](e.Disk.Path("status", s.Key()))
		if err == nil && status.State == "failed" {
			e.Log.Warn("last discovery failed; using previous inventory", "store", s.Key(), "error", status.Error)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			e.Log.Warn("cannot read discovery status", "store", s.Key(), "error", err)
		}

		refs = append(refs, snapshot.References...)
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })

	return refs, valid
}
