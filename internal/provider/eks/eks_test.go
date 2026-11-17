package eks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
)

func TestEKSPaginationFailsClosed(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/clusters":
			if r.URL.Query().Get("nextToken") == "" {
				_, _ = w.Write([]byte(`{"clusters":["one"],"nextToken":"page2"}`))
			} else if fail.Load() {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"denied"}`))
			} else {
				_, _ = w.Write([]byte(`{"clusters":["two"]}`))
			}
		case "/clusters/one", "/clusters/two":
			name := strings.TrimPrefix(r.URL.Path, "/clusters/")
			_ = json.NewEncoder(w).Encode(map[string]any{"cluster": map[string]any{"arn": "arn:aws:eks:region:account:cluster/" + name, "name": name}})
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer server.Close()

	cfg := aws.Config{Region: "region", BaseEndpoint: aws.String(server.URL), RetryMaxAttempts: 1, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
	})}
	client := eks.NewFromConfig(cfg)
	p := &EKS{Store: config.Store{Kind: "eks", ID: "test"}, ClientFactory: func(context.Context) (*eks.Client, aws.Config, error) { return client, cfg, nil }}

	refs, err := p.Discover(t.Context())
	require.Error(t, err)
	require.Nil(t, refs)

	fail.Store(false)
	refs, err = p.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, refs, 2)
}
