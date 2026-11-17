package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/namespacecache"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
	"go.yaml.in/yaml/v3"
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
	binary := testutil.Binary(t)
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
	otherOut, _, err := invoke(t, args...)
	require.NoError(t, err)
	otherPath := string(bytes.TrimSpace([]byte(otherOut)))
	t.Setenv("KUBECONFIG", path)
	command := exec.Command(binary, "--state-directory", root, "--machine", "ns", "changed")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	cfg, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)
	require.Equal(t, "changed", cfg.Contexts["test"].Namespace, "the calling shell must see the namespace change")
	other, err := clientcmd.LoadFromFile(otherPath)
	require.NoError(t, err)
	require.Equal(t, "saved", other.Contexts["test"].Namespace, "another shell's session must remain unchanged")
	last, err := disk.Last()
	require.NoError(t, err)
	require.Equal(t, "changed", last.Namespace)
	cache, err := namespacecache.New(disk, cfg)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		status, err := state.Read[state.Status](disk.Path("namespace-status", cache.Key))
		return err == nil && status.State == "failed"
	}, 5*time.Second, 10*time.Millisecond, "background failure must be recorded without blocking explicit selection")
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

func TestRancherSelectionUsesClusterName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("KUBECONFIG", "")

	const clusterID = "c-m-5fqkrgwg"
	const clusterName = "agones-dev"
	source, err := clientcmd.Load(testutil.Kubeconfig("saved"))
	require.NoError(t, err)
	source.Contexts = map[string]*clientcmdapi.Context{clusterID: source.Contexts[source.CurrentContext]}
	source.CurrentContext = clusterID
	data, err := clientcmd.Write(*source)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/management.cattle.io/v3/clusters":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "management.cattle.io/v3",
				"kind":       "ClusterList",
				"items": []map[string]any{{
					"metadata": map[string]string{"name": clusterID},
					"spec":     map[string]string{"displayName": clusterName},
				}},
			})
		case "/apis/ext.cattle.io/v1/kubeconfigs":
			var request struct {
				Spec struct {
					Clusters       []string `json:"clusters"`
					CurrentContext string   `json:"currentContext"`
				} `json:"spec"`
			}
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			assert.Equal(t, []string{clusterID}, request.Spec.Clusters)
			assert.Equal(t, clusterID, request.Spec.CurrentContext)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]string{"value": string(data)}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	settings := config.Config{
		Kind:    "SwitchConfig",
		Version: "v1alpha1",
		Stores: []config.Store{{
			Kind: "rancher",
			ID:   "test",
			Config: config.Settings{
				RancherAPIAddress: server.URL,
				RancherToken:      "test-token",
			},
		}},
	}
	configData, err := yaml.Marshal(settings)
	require.NoError(t, err)
	configPath := filepath.Join(dir, "switch.yaml")
	require.NoError(t, os.WriteFile(configPath, configData, 0600))
	stateDir := filepath.Join(dir, "state")
	args := []string{"--state-directory", stateDir, "--config-path", configPath, "--machine"}

	_, _, err = invoke(t, append(args, "discover")...)
	require.NoError(t, err)
	disk := state.Disk{Root: stateDir}
	resolved, err := config.Load(configPath, "", stateDir)
	require.NoError(t, err)
	for _, store := range resolved.Stores {
		if store.Kind != "rancher" {
			continue
		}

		snapshot, err := disk.Snapshot(store.Key(), store.Fingerprint())
		require.NoError(t, err)
		snapshot.References[0].Context = clusterID
		require.NoError(t, state.Atomic(disk.Path("inventory", store.Key()), snapshot))
	}

	out, _, err := invoke(t, append(args, "set-context", "rancher.test:"+clusterID)...)
	require.NoError(t, err)
	path := string(bytes.TrimSpace([]byte(out)))
	selected, err := clientcmd.LoadFromFile(path)
	require.NoError(t, err)
	require.Equal(t, clusterName, selected.CurrentContext)
	require.Len(t, selected.Contexts, 1)
	require.Equal(t, "saved", selected.Contexts[clusterName].Namespace)

	t.Setenv("KUBECONFIG", path)
	out, _, err = invoke(t, "current-context")
	require.NoError(t, err)
	require.Equal(t, clusterName+"\n", out)

	server.Close()
	require.NoError(t, os.WriteFile(configPath, []byte("not: [valid"), 0600))
	out, _, err = invoke(t, append(args, ".")...)
	require.NoError(t, err)
	restored, err := clientcmd.LoadFromFile(string(bytes.TrimSpace([]byte(out))))
	require.NoError(t, err)
	require.Equal(t, clusterName, restored.CurrentContext)
	require.Equal(t, "saved", restored.Contexts[clusterName].Namespace)
}

func TestCachedGKESelectionUsesClusterNameWithoutCloudCalls(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("KUBECONFIG", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "credentials-must-not-be-read"))

	configPath := filepath.Join(dir, "switch.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("kind: SwitchConfig\nversion: v1alpha1\nkubeconfigStores:\n- kind: gke\n  id: test\n  config:\n    projectIDs: [project]\n"), 0600))
	stateDir := filepath.Join(dir, "state")
	disk := state.Disk{Root: stateDir}
	resolved, err := config.Load(configPath, "", stateDir)
	require.NoError(t, err)
	const resource = "projects/project/locations/region/clusters/cluster-dev"
	for _, store := range resolved.Stores {
		if store.Kind != "gke" {
			continue
		}

		require.NoError(t, state.Atomic(disk.Path("inventory", store.Key()), state.Snapshot{
			Version: state.Version, Fingerprint: store.Fingerprint(), Complete: true, Discovered: time.Now(),
			References: []state.Reference{{
				ID: store.Key() + ":" + resource, Store: store.Key(),
				Name: store.Key() + "/gke_project_region_cluster-dev", Context: "project_region_cluster-dev",
				Metadata: map[string]string{"resource": resource, "endpoint": "https://127.0.0.1", "ca": "Y2E="},
			}},
		}))
	}

	out, _, err := invoke(t, "--state-directory", stateDir, "--config-path", configPath, "--machine", "set-context", "gke.test:"+resource)
	require.NoError(t, err)
	selected, err := clientcmd.LoadFromFile(string(bytes.TrimSpace([]byte(out))))
	require.NoError(t, err)
	require.Equal(t, "cluster-dev", selected.CurrentContext)
	require.Len(t, selected.Contexts, 1)
	require.Contains(t, selected.Contexts, "cluster-dev")
	require.Equal(t, "https://127.0.0.1", selected.Clusters[selected.Contexts["cluster-dev"].Cluster].Server)
	last, err := disk.Last()
	require.NoError(t, err)
	require.Equal(t, "cluster-dev", last.Context)
}
