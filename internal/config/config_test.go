package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCompatibleConfig(t *testing.T) {
	c, err := Decode([]byte(`kind: SwitchConfig
version: v1alpha1
refreshIndexAfter: 30m
kubeconfigStores:
- kind: gke
  config:
    Authentication:
      authenticationType: gcloud
    projectIDs: [one, two]
- kind: aks
  id: work
  required: false
  config:
    subscriptionID: sub
    resourceGroups: [dev]
`))
	require.NoError(t, err)
	require.NoError(t, c.Validate())
	require.Equal(t, "gcloud", c.Stores[0].Config.Authentication.Type)
	require.Equal(t, "azure.work", c.Stores[1].Key())
	require.Equal(t, 30*time.Minute, c.Stores[0].Interval)
	require.False(t, c.Stores[1].IsRequired())
}

func TestRejectInvalidConfig(t *testing.T) {
	for _, input := range []string{
		"kind: SwitchConfig\nversion: v1alpha1\nhooks: []",
		"kind: SwitchConfig\nversion: v1alpha1\n---\nkind: SwitchConfig",
		"kind: SwitchConfig\nversion: v1alpha1\nrefreshIndexAfter: -1s",
		"kind: SwitchConfig\nversion: v1alpha1\nkubeconfigStores: [{kind: vault}]",
		"kind: SwitchConfig\nversion: v1alpha1\nkubeconfigStores: [{kind: eks}, {kind: eks}]",
		"kind: SwitchConfig\nversion: v1alpha1\nkubeconfigStores: [{kind: gke, config: {authentication: {}, Authentication: {}}}]",
	} {
		t.Run(input, func(t *testing.T) {
			cfg, err := Decode([]byte(input))
			if err == nil {
				err = cfg.Validate()
			}
			require.Error(t, err)
		})
	}
}

func TestEnvironmentPathsExcludeSessions(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	t.Setenv("HOME", dir)
	t.Setenv("KUBECONFIG", filepath.Join(state, "sessions", "config")+":"+filepath.Join(dir, "source"))
	cfg, err := Load(filepath.Join(dir, "missing"), "", state)
	require.NoError(t, err)
	require.Len(t, cfg.Stores, 1)
	require.Equal(t, []string{filepath.Join(dir, "source")}, cfg.Stores[0].Paths)
	before := cfg.Stores[0].Fingerprint()
	cfg.Stores[0].Paths = append(cfg.Stores[0].Paths, "/another")
	require.NotEqual(t, before, cfg.Stores[0].Fingerprint())
	require.NoError(t, os.Unsetenv("KUBECTXMGR_UNSET_TEST"))
	_, err = Expand("${KUBECTXMGR_UNSET_TEST}/config")
	require.ErrorContains(t, err, "unset")
}

func TestRelativeConfigPathsAndWrongProviderFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("KUBECONFIG", "")
	path := filepath.Join(dir, "switch.yaml")
	require.NoError(t, os.WriteFile(path, []byte("kind: SwitchConfig\nversion: v1alpha1\nkubeconfigStores:\n- kind: filesystem\n  paths: [relative]\n"), 0600))
	cfg, err := Load(path, "", filepath.Join(dir, "state"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "relative"), cfg.Stores[0].Paths[0])
	cfg.Stores[0].Config.GCPAccount = "wrong-provider"
	require.ErrorContains(t, cfg.Validate(), "not supported")
}
