// Package provider defines discovery contracts and assembles provider implementations.
package provider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/provider/aks"
	"github.com/zoomoid/kubectxmgr/internal/provider/eks"
	"github.com/zoomoid/kubectxmgr/internal/provider/filesystem"
	"github.com/zoomoid/kubectxmgr/internal/provider/gke"
	"github.com/zoomoid/kubectxmgr/internal/provider/rancher"
	"github.com/zoomoid/kubectxmgr/internal/provider/shared"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// Provider separates metadata discovery from potentially credential-issuing work.
type Provider interface {
	// Discover returns all visible references in the configured scope or an error.
	// Partial results must not be reported as a successful inventory.
	Discover(context.Context) ([]state.Reference, error)

	// Materialize obtains kubeconfig bytes for a previously discovered reference.
	// Normal discovery may prefetch private AKS entries; execution discovery and
	// dry-run must never call this method.
	Materialize(context.Context, state.Reference) ([]byte, error)
}

// Factory constructs the provider for one validated store without performing discovery.
type Factory func(config.Store) (Provider, error)

// NewEntry adapts cached inventory for display without configuration or API calls.
func NewEntry(ref state.Reference) shared.Entry {
	entry := shared.NewEntry(ref)
	kind, _, _ := strings.Cut(ref.Store, ".")
	switch kind {
	case "gke":
		return gke.Entry{ReferenceEntry: entry}
	case "rancher":
		return rancher.Entry{ReferenceEntry: entry}
	default:
		return entry
	}
}

// NewFactory wires providers with automatic AKS and optional credential caching.
// Filesystem discovery excludes application state and supplied cache directories;
// Rancher manages its own short-lived cache to avoid duplicate token issuance.
func NewFactory(disk state.Disk, log *slog.Logger, excluded ...string) Factory {
	return func(s config.Store) (Provider, error) {
		var p Provider
		switch s.Kind {
		case "filesystem":
			p = &filesystem.Filesystem{Store: s, Exclude: disk.Root, ExcludePaths: excluded}
		case "gke":
			p = &gke.GKE{Store: s}
		case "eks":
			p = &eks.EKS{Store: s}
		case "azure":
			p = &aks.AKS{Store: s}
		case "rancher":
			if s.Config.LegacyAPI {
				log.Warn("Rancher legacy v3 API is unsupported by Rancher", "store", s.Key())
			}
			return &rancher.Rancher{Store: s, Disk: disk}, nil
		default:
			return nil, fmt.Errorf("unsupported store %s", s.Kind)
		}

		if s.Cache != nil || s.Kind == "azure" {
			p = cached{Provider: p, store: s, disk: disk}
		}

		return p, nil
	}
}
