package rancher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestRancherGeneratedCredentialLifetime(t *testing.T) {
	for _, ttl := range []int64{3600, 30} {
		t.Run((time.Duration(ttl) * time.Second).String(), func(t *testing.T) {
			testRancherGeneratedCredentialLifetime(t, ttl)
		})
	}
}

func testRancherGeneratedCredentialLifetime(t *testing.T, serverTTL int64) {
	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Spec struct {
				TTL *int64 `json:"ttl"`
			} `json:"spec"`
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ttl := serverTTL
		if request.Spec.TTL != nil {
			ttl = *request.Spec.TTL
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"spec":   map[string]any{"ttl": ttl},
			"status": map[string]any{"value": string(testutil.Kubeconfig("ns"))},
		})
	}))
	defer server.Close()

	p := &Rancher{Store: config.Store{Kind: "rancher", ID: "one", Config: config.Settings{RancherAPIAddress: server.URL, RancherToken: "test-token"}}, Disk: state.Disk{Root: t.TempDir()}}
	ref := state.Reference{ID: "one", Metadata: map[string]string{"cluster": "one"}}
	_, err := p.Materialize(t.Context(), ref)
	require.NoError(t, err)
	cache, err := state.Read[state.Credentials](p.Disk.Path("credentials", ref.ID+p.Store.Fingerprint()))
	require.NoError(t, err)
	ttl := time.Duration(serverTTL) * time.Second
	require.True(t, cache.Expires.After(now.Add(ttl/2)), "credential cache must not expire prematurely")
	require.True(t, cache.Expires.Before(now.Add(ttl)), "cache must expire before the issued credential")
}

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
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec":   map[string]any{"ttl": 3600},
				"status": map[string]any{"value": string(testutil.Kubeconfig("ns"))},
			})
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

func TestRancherACERejectsProxyOnlyKubeconfig(t *testing.T) {
	cfg, err := clientcmd.Load(testutil.Kubeconfig("ns"))
	require.NoError(t, err)
	proxy := cfg.Contexts[cfg.CurrentContext]
	cfg.Clusters[proxy.Cluster].Server = "https://rancher.example/k8s/clusters/downstream"
	cfg.Clusters["rancher"] = &clientcmdapi.Cluster{Server: "https://rancher.example"}
	cfg.Contexts["rancher"] = &clientcmdapi.Context{Cluster: "rancher", AuthInfo: proxy.AuthInfo}
	data, err := clientcmd.Write(*cfg)
	require.NoError(t, err)

	_, err = selectAuthorizedClusterEndpoint(data, "downstream")
	require.ErrorContains(t, err, "no authorized cluster endpoint")
}
