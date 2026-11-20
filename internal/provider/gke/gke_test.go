package gke

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	pb "cloud.google.com/go/container/apiv1/containerpb"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/tools/clientcmd"
)

type gkeServer struct {
	pb.UnimplementedClusterManagerServer
	missing atomic.Bool
	gets    atomic.Int32
}

func (s *gkeServer) ListClusters(_ context.Context, r *pb.ListClustersRequest) (*pb.ListClustersResponse, error) {
	if r.GetParent() == "projects/denied/locations/-" {
		return nil, status.Error(codes.PermissionDenied, "container.clusters.list denied")
	}

	result := &pb.ListClustersResponse{Clusters: []*pb.Cluster{{
		Name: "one", Location: "region", Endpoint: "127.0.0.1",
		MasterAuth: &pb.MasterAuth{ClusterCaCertificate: "Y2E="},
	}}}
	if s.missing.Load() || r.GetParent() == "projects/partial/locations/-" {
		result.MissingZones = []string{"unavailable-zone"}
	}

	return result, nil
}

func TestGKEDiscoverySkipsFailedProjects(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pb.RegisterClusterManagerServer(server, &gkeServer{})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	var logs bytes.Buffer
	p := &GKE{
		Store:         config.Store{Kind: "gke", ID: "test", Config: config.Settings{ProjectIDs: []string{"denied", "partial", "healthy"}}},
		ClientOptions: []option.ClientOption{option.WithGRPCConn(connection)},
		Log:           slog.New(slog.NewTextHandler(&logs, nil)),
	}

	refs, err := p.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Contains(t, refs[0].ID, "projects/healthy/")
	require.Contains(t, logs.String(), "project=denied")
	require.Contains(t, logs.String(), "PermissionDenied")
	require.Contains(t, logs.String(), "project=partial")
	require.Contains(t, logs.String(), "missing zones")
}

func (s *gkeServer) GetCluster(context.Context, *pb.GetClusterRequest) (*pb.Cluster, error) {
	s.gets.Add(1)

	return &pb.Cluster{
		Name:       "one",
		Location:   "region",
		Endpoint:   "127.0.0.1",
		MasterAuth: &pb.MasterAuth{ClusterCaCertificate: "Y2E="},
	}, nil
}

func TestGKEMissingZonesAreIncomplete(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	implementation := &gkeServer{}
	implementation.missing.Store(true)
	pb.RegisterClusterManagerServer(server, implementation)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	p := &GKE{Store: config.Store{Kind: "gke", ID: "test", Config: config.Settings{ProjectIDs: []string{"project"}}}, ClientOptions: []option.ClientOption{option.WithGRPCConn(connection)}}
	refs, err := p.Discover(t.Context())
	require.ErrorContains(t, err, "missing zones")
	require.Nil(t, refs)

	implementation.missing.Store(false)
	connection, err = grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	p.ClientOptions = []option.ClientOption{option.WithGRPCConn(connection)}
	refs, err = p.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Contains(t, refs[0].ID, "projects/project/locations/region/clusters/one")
	require.Equal(t, "one", refs[0].Context)
	require.Equal(t, "gke.test/gke_project_region_one", refs[0].Name)

	started := time.Now()
	data, err := p.Materialize(t.Context(), refs[0])
	t.Logf("materializing discovered GKE reference: %s", time.Since(started))
	require.NoError(t, err)
	cfg, err := clientcmd.Load(data)
	require.NoError(t, err)
	require.Equal(t, "one", cfg.CurrentContext)
	require.Contains(t, cfg.Contexts, "one")
	require.Zero(t, implementation.gets.Load(), "selection must reuse discovered connection data instead of waiting for GetCluster")

	legacy := refs[0]
	legacy.Metadata = map[string]string{"resource": refs[0].Metadata["resource"]}
	connection, err = grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	p.ClientOptions = []option.ClientOption{option.WithGRPCConn(connection)}
	_, err = p.Materialize(t.Context(), legacy)
	require.NoError(t, err, "old inventories must still work before rediscovery")
	require.EqualValues(t, 1, implementation.gets.Load())

	_, err = gkeEndpoint(&pb.Cluster{Endpoint: "public"}, "private")
	require.Error(t, err)
	endpoint, err := gkeEndpoint(&pb.Cluster{Endpoint: "public"}, "public")
	require.NoError(t, err)
	require.Equal(t, "https://public", endpoint)
}

func TestCachedGKEConnectionValidation(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", t.TempDir()+"/must-not-be-read")
	for _, tt := range []struct {
		name, preference, ca, wantError string
	}{
		{name: "IP endpoint", ca: "Y2E="},
		{name: "DNS endpoint", preference: "dns"},
		{name: "missing IP CA", wantError: "invalid endpoint/CA metadata"},
		{name: "invalid CA", ca: "!", wantError: "illegal base64"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &GKE{Store: config.Store{Config: config.Settings{PreferredEndpoint: tt.preference}}}
			data, err := p.Materialize(t.Context(), state.Reference{
				Context: "one", Metadata: map[string]string{"endpoint": "https://cluster.example", "ca": tt.ca},
			})
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}

			require.NoError(t, err)
			cfg, err := clientcmd.Load(data)
			require.NoError(t, err)
			require.Equal(t, "https://cluster.example", cfg.Clusters["one"].Server)
			require.Equal(t, "gke-gcloud-auth-plugin", cfg.AuthInfos["one"].Exec.Command)
			require.Equal(t, []string{"--use_application_default_credentials"}, cfg.AuthInfos["one"].Exec.Args)
		})
	}
}
