package gke

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	pb "cloud.google.com/go/container/apiv1/containerpb"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type gkeServer struct {
	pb.UnimplementedClusterManagerServer
	missing atomic.Bool
}

func (s *gkeServer) ListClusters(_ context.Context, r *pb.ListClustersRequest) (*pb.ListClustersResponse, error) {
	result := &pb.ListClustersResponse{Clusters: []*pb.Cluster{{Name: "one", Location: "region"}}}
	if s.missing.Load() {
		result.MissingZones = []string{"unavailable-zone"}
	}

	return result, nil
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

	_, err = gkeEndpoint(&pb.Cluster{Endpoint: "public"}, "private")
	require.Error(t, err)
	endpoint, err := gkeEndpoint(&pb.Cluster{Endpoint: "public"}, "public")
	require.NoError(t, err)
	require.Equal(t, "https://public", endpoint)
}
