// Package namespacecache persists per-cluster namespace inventories and usage.
package namespacecache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/zoomoid/kubectxmgr/internal/state"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

// Snapshot keeps exhaustive names separate from local namespace usage.
type Snapshot struct {
	Version   int                  `yaml:"version"`
	Refreshed time.Time            `yaml:"refreshedAt"`
	Names     []string             `yaml:"names"`
	LastUsed  map[string]time.Time `yaml:"lastUsed"`
}

// Cache addresses one cluster independently of context aliases and session files.
type Cache struct {
	Disk state.Disk
	Key  string
}

// New derives a private cache key from the cluster's public connection settings.
func New(disk state.Disk, cfg *api.Config) (Cache, error) {
	current := cfg.Contexts[cfg.CurrentContext]
	if current == nil || cfg.Clusters[current.Cluster] == nil {
		return Cache{}, errors.New("no active kubeconfig cluster")
	}

	cluster := cfg.Clusters[current.Cluster].DeepCopy()
	cluster.LocationOfOrigin = ""
	if cluster.CertificateAuthority != "" {
		data, err := os.ReadFile(cluster.CertificateAuthority)
		if err != nil {
			return Cache{}, err
		}

		cluster.CertificateAuthorityData = data
		cluster.CertificateAuthority = ""
	}

	data, err := json.Marshal(cluster)
	if err != nil {
		return Cache{}, err
	}

	return Cache{Disk: disk, Key: state.Hash(string(data))}, nil
}

// Load reads a snapshot without waiting for refresh or usage writers.
func (c Cache) Load() (Snapshot, error) {
	snapshot, err := state.Read[Snapshot](c.Disk.Path("namespaces", c.Key))
	if err != nil {
		return snapshot, err
	}

	if snapshot.Version != state.Version {
		return snapshot, errors.New("incompatible namespace cache")
	}

	return snapshot, nil
}

// References orders known namespaces by most recent use, then alphabetically.
func (s Snapshot) References() []state.Reference {
	refs := make([]state.Reference, 0, len(s.Names))
	for _, name := range s.Names {
		refs = append(refs, state.Reference{ID: name, Name: name})
	}

	sort.SliceStable(refs, func(i, j int) bool {
		a, b := s.LastUsed[refs[i].Name], s.LastUsed[refs[j].Name]
		if !a.Equal(b) {
			return a.After(b)
		}

		return refs[i].Name < refs[j].Name
	})

	return refs
}

// RecordUse merges a successful selection without replacing namespace discovery.
func (c Cache) RecordUse(ctx context.Context, name string) error {
	return c.update(ctx, func(snapshot *Snapshot) {
		if snapshot.LastUsed == nil {
			snapshot.LastUsed = make(map[string]time.Time)
		}

		snapshot.LastUsed[name] = time.Now().UTC()
	})
}

// update serializes short atomic writes, merging usage recorded during API calls.
func (c Cache) update(ctx context.Context, change func(*Snapshot)) error {
	unlock, err := c.Disk.Lock(ctx, "namespace-cache-"+c.Key, true)
	if err != nil {
		return err
	}
	defer unlock()

	snapshot, err := c.Load()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	snapshot.Version = state.Version
	change(&snapshot)

	return state.Atomic(c.Disk.Path("namespaces", c.Key), snapshot)
}

// Refresh exhausts namespace pages before publishing. Failed scans leave the
// previous inventory intact; only one refresh per cluster can run at a time.
func (c Cache) Refresh(ctx context.Context, cfg *api.Config, wait bool) error {
	unlock, err := c.Disk.Lock(ctx, "namespace-refresh-"+c.Key, wait)
	if err != nil {
		return err
	}
	defer unlock()

	status := state.Status{Version: state.Version, Attempt: time.Now().UTC(), State: "running"}
	path := c.Disk.Path("namespace-status", c.Key)
	if err := state.Atomic(path, status); err != nil {
		return err
	}

	names, scanErr := list(ctx, cfg)
	if scanErr == nil {
		scanErr = c.update(ctx, func(snapshot *Snapshot) {
			snapshot.Names = names
			snapshot.Refreshed = time.Now().UTC()
		})
	}

	status.State = "succeeded"
	if scanErr != nil {
		status.State = "failed"
		status.Error = scanErr.Error()
	}

	return errors.Join(scanErr, state.Atomic(path, status))
}

// list uses the selected cluster's credentials without reading provider configuration.
func list(ctx context.Context, cfg *api.Config) ([]string, error) {
	restConfig, err := clientcmd.NewDefaultClientConfig(*cfg, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	restConfig.Timeout = 15 * time.Second

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	var names []string
	token := ""
	for {
		result, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 100, Continue: token})
		if err != nil {
			return nil, fmt.Errorf("list namespaces (or specify one explicitly): %w", err)
		}

		for _, item := range result.Items {
			names = append(names, item.Name)
		}

		token = result.Continue
		if token == "" {
			return names, nil
		}
	}
}
