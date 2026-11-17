package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func invoke(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := New()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(t.Context())
	return out.String(), stderr.String(), err
}

func TestRestoreOfflineAndNamespaceIsolation(t *testing.T) {
	root := t.TempDir()
	disk := state.Disk{Root: root}
	require.NoError(t, disk.SaveSelection(t.Context(), state.Selection{Context: "test", Namespace: "saved", Kubeconfig: testutil.Kubeconfig("saved")}))
	unlock, err := disk.Lock(t.Context(), "discover-filesystem.default", true)
	require.NoError(t, err)
	defer unlock()
	invalidConfig := filepath.Join(root, "invalid.yaml")
	require.NoError(t, os.WriteFile(invalidConfig, []byte("not: [valid"), 0600))
	args := []string{"--state-directory", root, "--config-path", invalidConfig, "--machine", "."}
	out, _, err := invoke(t, args...)
	require.NoError(t, err, "restore must not parse provider configuration or wait for discovery")
	path := string(bytes.TrimSpace([]byte(out)))
	t.Setenv("KUBECONFIG", path)
	_, _, err = invoke(t, "--state-directory", root, "--machine", "ns", "changed")
	require.NoError(t, err)
	cfg, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)
	require.Equal(t, "saved", cfg.Contexts["test"].Namespace, "other session copy remains unchanged")
	last, err := disk.Last()
	require.NoError(t, err)
	require.Equal(t, "changed", last.Namespace)
}

func TestDryRunDoesNotMaterializeOrRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("KUBECONFIG", "")
	source := filepath.Join(dir, "config")
	marker := filepath.Join(dir, "must-not-exist")
	require.NoError(t, os.WriteFile(source, testutil.Kubeconfig("source-ns"), 0600))
	out, _, err := invoke(t, "--state-directory", filepath.Join(dir, "state"), "--kubeconfig-path", source, "exec", "--dry-run", "--json", "*", "--", "touch", marker)
	require.NoError(t, err)
	require.Contains(t, out, `"namespace":"source-ns"`)
	_, err = os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(dir, "state", "last.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(dir, "state", "sessions"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestIncompleteOptionalStoreBlocksExecution(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("KUBECONFIG", "")
	configPath := filepath.Join(dir, "switch.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("kind: SwitchConfig\nversion: v1alpha1\nkubeconfigStores:\n- kind: filesystem\n  required: false\n  paths: ["+filepath.Join(dir, "missing")+"]\n"), 0600))
	_, _, err := invoke(t, "--state-directory", filepath.Join(dir, "state"), "--config-path", configPath, "exec", "--dry-run", "*", "--", "true")
	require.ErrorContains(t, err, "fresh complete discovery")
}

func TestRestoreDoesNotExecuteCredentialHelpers(t *testing.T) {
	root := t.TempDir()
	cfg, err := clientcmd.Load(testutil.Kubeconfig("ns"))
	require.NoError(t, err)
	cfg.AuthInfos["user"].Exec = &clientcmdapi.ExecConfig{Command: "must-not-run", APIVersion: "client.authentication.k8s.io/v1", InteractiveMode: clientcmdapi.NeverExecInteractiveMode}
	data, err := clientcmd.Write(*cfg)
	require.NoError(t, err)
	d := state.Disk{Root: root}
	require.NoError(t, d.SaveSelection(t.Context(), state.Selection{Context: "test", Namespace: "ns", Kubeconfig: data}))
	_, _, err = invoke(t, "--state-directory", root, "--machine", "restore")
	require.NoError(t, err)
}
