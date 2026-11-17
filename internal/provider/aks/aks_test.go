package aks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	aks "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
)

type testCredential struct{}

func (testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestAKSExhaustsPagesAndReportsErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"Forbidden","message":"page two denied"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"value":[{"id":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ContainerService/managedClusters/one","name":"one"}],"nextLink":"https://` + r.Host + r.URL.Path + `?page=2"}`))
	}))
	defer server.Close()
	c := cloud.Configuration{ActiveDirectoryAuthorityHost: cloud.AzurePublic.ActiveDirectoryAuthorityHost, Services: map[cloud.ServiceName]cloud.ServiceConfiguration{cloud.ResourceManager: {Endpoint: server.URL, Audience: server.URL}}}
	client, err := aks.NewManagedClustersClient("sub", testCredential{}, &arm.ClientOptions{ClientOptions: azcore.ClientOptions{Cloud: c, Transport: server.Client(), Retry: policy.RetryOptions{MaxRetries: -1}}})
	require.NoError(t, err)
	p := &AKS{Store: config.Store{Kind: "azure", ID: "test"}, Client: client}
	refs, err := p.Discover(t.Context())
	require.ErrorContains(t, err, "Forbidden")
	require.Nil(t, refs)
}
