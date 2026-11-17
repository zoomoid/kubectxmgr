package namespacecache

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
	"k8s.io/client-go/tools/clientcmd"
)

func TestRefreshPreservesUsageAndFailedPages(t *testing.T) {
	var fail atomic.Bool
	requested, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continue") == "" {
			select {
			case requested <- struct{}{}:
			default:
			}
			<-release
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "v1", "kind": "NamespaceList", "metadata": map[string]string{"continue": "page2"},
				"items": []map[string]any{{"metadata": map[string]string{"name": "alpha"}}},
			})
		} else if fail.Load() {
			http.Error(w, "denied", http.StatusForbidden)
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiVersion": "v1", "kind": "NamespaceList",
				"items": []map[string]any{{"metadata": map[string]string{"name": "recent"}}},
			})
		}
	}))
	defer server.Close()
	cfg, err := clientcmd.Load(testutil.Kubeconfig("default"))
	require.NoError(t, err)
	cfg.Clusters["cluster"].Server = server.URL
	cache, err := New(state.Disk{Root: t.TempDir()}, cfg)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- cache.Refresh(t.Context(), cfg, false) }()
	<-requested
	require.NoError(t, cache.RecordUse(t.Context(), "alpha"))
	require.NoError(t, cache.RecordUse(t.Context(), "recent"), "slow API calls must not hold usage locks")
	require.ErrorIs(t, cache.Refresh(t.Context(), cfg, false), state.ErrBusy)
	close(release)
	require.NoError(t, <-done)
	snapshot, err := cache.Load()
	require.NoError(t, err)
	require.Equal(t, []state.Reference{{ID: "recent", Name: "recent"}, {ID: "alpha", Name: "alpha"}}, snapshot.References())

	fail.Store(true)
	require.Error(t, cache.Refresh(t.Context(), cfg, false))
	after, err := cache.Load()
	require.NoError(t, err)
	require.Equal(t, snapshot, after, "partial refresh must not replace the exhaustive cache or usage")

	alias := cfg.DeepCopy()
	alias.Contexts["alias"] = alias.Contexts[alias.CurrentContext]
	alias.CurrentContext = "alias"
	alias.Clusters["cluster"].LocationOfOrigin = "/another/session"
	same, err := New(cache.Disk, alias)
	require.NoError(t, err)
	require.Equal(t, cache.Key, same.Key)
	alias.Clusters["cluster"].Server = "https://different-cluster.example"
	other, err := New(cache.Disk, alias)
	require.NoError(t, err)
	require.NotEqual(t, cache.Key, other.Key)
}

func TestDetachedRefreshSurvivesParentExit(t *testing.T) {
	binary := testutil.Binary(t)
	requested, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requested)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"NamespaceList","items":[{"metadata":{"name":"background"}}]}`))
	}))
	defer server.Close()
	cfg, err := clientcmd.Load(testutil.Kubeconfig("default"))
	require.NoError(t, err)
	cfg.Clusters["cluster"].Server = server.URL
	dir := t.TempDir()
	source := filepath.Join(dir, "config")
	data, err := clientcmd.Write(*cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(source, data, 0600))
	t.Setenv("KUBECONFIG", source)
	cache, err := New(state.Disk{Root: filepath.Join(dir, "state")}, cfg)
	require.NoError(t, err)

	// The foreground CLI must return while its detached worker is still blocked.
	cmd := exec.Command(binary, "--state-directory", cache.Disk.Root, "--machine", "namespace", "recent")
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("foreground namespace command waited for the API")
	}
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("background worker did not query namespaces")
	}
	close(release)
	require.Eventually(t, func() bool {
		snapshot, err := cache.Load()
		status, statusErr := state.Read[state.Status](cache.Disk.Path("namespace-status", cache.Key))
		return err == nil && statusErr == nil && status.State == "succeeded" &&
			len(snapshot.Names) == 1 && snapshot.Names[0] == "background" &&
			!snapshot.LastUsed["recent"].IsZero()
	}, 5*time.Second, 10*time.Millisecond)
}
