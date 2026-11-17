package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azure "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/logging"
	"github.com/zoomoid/kubectxmgr/internal/provider/aks"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
)

type testAzureCredential struct{}

func (testAzureCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestAKSAutomaticallyCachesUserKubeconfigs(t *testing.T) {
	var calls atomic.Int32
	data := testutil.Kubeconfig("default")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/listClusterUserCredential") || r.URL.Query().Get("format") != "exec" {
			http.Error(w, "expected user credentials in exec format", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kubeconfigs": []map[string]any{{"name": "clusterUser", "value": data}},
		})
	}))
	defer server.Close()
	cloudConfig := cloud.Configuration{
		ActiveDirectoryAuthorityHost: cloud.AzurePublic.ActiveDirectoryAuthorityHost,
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: server.URL, Audience: server.URL},
		},
	}
	client, err := azure.NewManagedClustersClient("sub", testAzureCredential{}, &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{Cloud: cloudConfig, Transport: server.Client()},
	})
	require.NoError(t, err)

	disk := state.Disk{Root: t.TempDir()}
	store := config.Store{Kind: "azure", ID: "test", Config: config.Settings{SubscriptionID: "sub"}}
	factory := NewFactory(disk, logging.New(io.Discard, false, false))
	p, err := factory(store)
	require.NoError(t, err)
	base := p
	if c, ok := p.(cached); ok {
		base = c.Provider
	}
	base.(*aks.AKS).Client = client
	ref := state.Reference{ID: "azure.test:cluster", Store: store.Key(), Metadata: map[string]string{
		"resourceGroup": "rg", "name": "cluster",
	}}

	for range 2 {
		got, err := p.Materialize(t.Context(), ref)
		require.NoError(t, err)
		require.Equal(t, data, got)
	}
	require.EqualValues(t, 1, calls.Load(), "a valid cached selection must not request Azure credentials again")

	// A new command has no injected Azure client and must still select offline.
	p, err = factory(store)
	require.NoError(t, err)
	got, err := p.Materialize(t.Context(), ref)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.EqualValues(t, 1, calls.Load())

	path := disk.Path("credentials", ref.ID+store.Fingerprint())
	entry, err := state.Read[state.Credentials](path)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	entry.Expires = time.Now().Add(-time.Minute)
	require.NoError(t, state.Atomic(path, entry))
	p.(cached).Provider.(*aks.AKS).Client = client
	got, err = p.Materialize(t.Context(), ref)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.EqualValues(t, 2, calls.Load(), "expired entries must be refreshed")

	store.Cache = &config.Cache{Kind: "filesystem"}
	store.Cache.Config.Path = t.TempDir()
	p, err = factory(store)
	require.NoError(t, err)
	p.(cached).Provider.(*aks.AKS).Client = client
	_, err = p.Materialize(t.Context(), ref)
	require.NoError(t, err)
	customDisk := state.Disk{Root: store.Cache.Config.Path}
	_, err = state.Read[state.Credentials](customDisk.Path("credentials", ref.ID+store.Fingerprint()))
	require.NoError(t, err, "an explicitly configured credential-cache directory must be honored")
}
