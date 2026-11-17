// Package filesystem discovers contexts from local kubeconfig files.
package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
	"github.com/zoomoid/kubectxmgr/internal/provider/shared"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"k8s.io/client-go/tools/clientcmd"
)

// Filesystem discovers local kubeconfigs while excluding tool-owned state/cache trees.
type Filesystem struct {
	Store        config.Store
	Exclude      string
	ExcludePaths []string
}

// Discover traverses configured paths and reads every context without authenticating.
// Explicit files bypass filename filtering; resolved paths prevent symlink cycles
// and duplicate discovery. Any unreadable or invalid matching file fails the store.
func (p *Filesystem) Discover(ctx context.Context) ([]state.Reference, error) {
	var refs []state.Reference
	excluded := append([]string(nil), p.ExcludePaths...)
	if p.Exclude != "" {
		excluded = append(excluded, p.Exclude)
	}

	for i, path := range excluded {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			excluded[i] = resolved
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	visited := map[string]bool{}
	var visit func(string, bool) error
	visit = func(path string, explicit bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}

		if visited[resolved] {
			return nil
		}

		for _, path := range excluded {
			if config.OwnedPath(path, resolved) {
				return nil
			}
		}

		visited[resolved] = true
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}

		if info.IsDir() {
			entries, err := os.ReadDir(resolved)
			if err != nil {
				return err
			}

			for _, entry := range entries {
				if err := visit(filepath.Join(resolved, entry.Name()), false); err != nil {
					return err
				}
			}

			return nil
		}

		match, err := filepath.Match(p.Store.KubeconfigName, filepath.Base(path))
		if err != nil {
			return err
		}

		if !explicit && !match {
			return nil
		}

		cfg, err := clientcmd.LoadFromFile(resolved)
		if err != nil {
			return err
		}

		for name, c := range cfg.Contexts {
			if c == nil || cfg.Clusters[c.Cluster] == nil || (c.AuthInfo != "" && cfg.AuthInfos[c.AuthInfo] == nil) {
				return fmt.Errorf("%s: context %s references missing cluster/user", path, name)
			}

			r := shared.Reference(p.Store, resolved+"#"+name, name, map[string]string{"path": resolved, "context": name})
			if p.Store.ShowPrefix == nil || *p.Store.ShowPrefix {
				r.Name = filepath.Base(filepath.Dir(resolved)) + "/" + name
			}

			r.Namespace = c.Namespace
			if r.Namespace == "" {
				r.Namespace = "default"
			}

			refs = append(refs, r)
		}

		return nil
	}

	for _, path := range p.Store.Paths {
		if err := visit(path, true); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })

	return refs, nil
}

// Materialize reloads the source file with credential paths anchored to that file.
func (p *Filesystem) Materialize(_ context.Context, r state.Reference) ([]byte, error) {
	return kubeconfig.File(r.Metadata["path"])
}
