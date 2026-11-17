// Package kubeconfig builds private, self-contained configs without executing auth plugins.
package kubeconfig

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zoomoid/kubectxmgr/internal/state"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

// Select isolates one context, optionally changes its namespace, and embeds local
// certificate/key files. An empty name uses current-context; an empty namespace
// preserves the context's namespace, defaulting to "default" when absent.
func Select(data []byte, name, namespace string) ([]byte, string, error) {
	cfg, err := clientcmd.Load(data)
	if err != nil {
		return nil, "", err
	}

	if name == "" {
		name = cfg.CurrentContext
	}

	c, ok := cfg.Contexts[name]
	if !ok {
		return nil, "", fmt.Errorf("kubeconfig has no context %q", name)
	}

	if namespace != "" {
		c.Namespace = namespace
	}

	if c.Namespace == "" {
		c.Namespace = "default"
	}

	cfg.CurrentContext = name
	if err := api.MinifyConfig(cfg); err != nil {
		return nil, "", err
	}

	if err := api.FlattenConfig(cfg); err != nil {
		return nil, "", err
	}

	b, err := clientcmd.Write(*cfg)
	return b, c.Namespace, err
}

// File loads a source config and anchors local credential paths to its directory.
func File(path string) ([]byte, error) {
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, err
	}

	if err := clientcmd.ResolveLocalPaths(cfg); err != nil {
		return nil, err
	}

	return clientcmd.Write(*cfg)
}

// SetNamespace updates only the active context in its original kubeconfig file.
// The client-go writer preserves merged-file origins and coordinates file locks.
func SetNamespace(expectedContext, namespace string) error {
	access := clientcmd.NewDefaultPathOptions()
	cfg, err := access.GetStartingConfig()
	if err != nil {
		return err
	}

	if cfg.CurrentContext != expectedContext {
		return errors.New("active context changed during namespace selection; try again")
	}

	current := cfg.Contexts[cfg.CurrentContext]
	if current == nil {
		return errors.New("no active kubeconfig context")
	}

	current.Namespace = namespace

	return clientcmd.ModifyConfig(access, *cfg, false)
}

// Generate builds a single-context config using an exec credential plugin.
// ca is base64-encoded; an empty CA uses the operating system's trust roots.
func Generate(name, endpoint, ca string, exec *api.ExecConfig) ([]byte, error) {
	if endpoint == "" {
		return nil, errors.New("provider returned an empty endpoint")
	}

	cfg := api.NewConfig()
	cfg.Clusters[name] = &api.Cluster{Server: endpoint}
	cfg.AuthInfos[name] = &api.AuthInfo{Exec: exec}
	cfg.Contexts[name] = &api.Context{Cluster: name, AuthInfo: name}
	cfg.CurrentContext = name

	data, err := base64.StdEncoding.DecodeString(ca)
	if err != nil {
		return nil, err
	}

	cfg.Clusters[name].CertificateAuthorityData = data

	return clientcmd.Write(*cfg)
}

// Session writes a unique private kubeconfig and returns its path.
// The caller owns cleanup: shell sessions retain it, while command execution
// removes its copy after the child exits.
func Session(root string, data []byte) (string, error) {
	dir := filepath.Join(root, "sessions")
	if err := state.PrivateDir(dir); err != nil {
		return "", err
	}

	f, err := os.CreateTemp(dir, "config-")
	if err != nil {
		return "", err
	}

	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}

	if err := state.Write(path, data); err != nil {
		_ = os.Remove(path)
		return "", err
	}

	return path, nil
}
