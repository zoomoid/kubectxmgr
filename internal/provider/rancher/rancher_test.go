package rancher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
	"k8s.io/client-go/tools/clientcmd"
)

func TestRancherRKAPIAndCredentialCache(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/management.cattle.io/v3/clusters":
			if r.URL.Query().Get("continue") == "" {
				_, _ = w.Write([]byte(`{"apiVersion":"management.cattle.io/v3","kind":"ClusterList","metadata":{"continue":"next"},"items":[{"metadata":{"name":"local"},"spec":{"displayName":"local"}}]}`))
			} else {
				_, _ = w.Write([]byte(`{"apiVersion":"management.cattle.io/v3","kind":"ClusterList","metadata":{},"items":[{"metadata":{"name":"downstream"},"spec":{"displayName":"dev"}}]}`))
			}
		case "/apis/ext.cattle.io/v1/kubeconfigs":
			assert.Equal(t, http.MethodPost, r.Method)
			posts.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"value": string(testutil.Kubeconfig("ns"))}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	p := &Rancher{Store: config.Store{Kind: "rancher", ID: "one", Config: config.Settings{RancherAPIAddress: server.URL + "/v3", RancherToken: "test-token"}}, Disk: state.Disk{Root: t.TempDir()}}
	refs, err := p.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, refs, 2)
	require.Zero(t, posts.Load(), "discovery must not issue tokens")
	require.Equal(t, "dev", refs[1].Context)
	require.Equal(t, "downstream", refs[1].Metadata["cluster"])

	for range 2 {
		data, err := p.Materialize(t.Context(), refs[0])
		require.NoError(t, err)
		cfg, err := clientcmd.Load(data)
		require.NoError(t, err)
		require.Equal(t, "test", cfg.CurrentContext)
	}

	require.EqualValues(t, 1, posts.Load())
}

func TestRancherDoesNotRetryCredentialCreation(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	p := &Rancher{Store: config.Store{Kind: "rancher", ID: "one", Config: config.Settings{RancherAPIAddress: server.URL, RancherToken: "test-token"}}, Disk: state.Disk{Root: t.TempDir()}}
	_, err := p.Materialize(t.Context(), state.Reference{ID: "one", Metadata: map[string]string{"cluster": "one"}})
	require.ErrorContains(t, err, "not retried")
	require.EqualValues(t, 1, posts.Load())
}
