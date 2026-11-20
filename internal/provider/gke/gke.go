// Package gke discovers Google Kubernetes Engine clusters and configures GKE authentication.
package gke

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"cloud.google.com/go/auth/credentials"
	container "cloud.google.com/go/container/apiv1"
	pb "cloud.google.com/go/container/apiv1/containerpb"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	resourcepb "cloud.google.com/go/resourcemanager/apiv3/resourcemanagerpb"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
	"github.com/zoomoid/kubectxmgr/internal/provider/shared"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"golang.org/x/oauth2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	api "k8s.io/client-go/tools/clientcmd/api"
)

// GKE discovers clusters in explicit projects or active projects visible to its identity.
type GKE struct {
	Store         config.Store
	ClientOptions []option.ClientOption
	Log           *slog.Logger
}

// Entry presents a GKE cluster independently of its generated kubeconfig context.
type Entry struct {
	shared.ReferenceEntry
}

// Label extracts the cluster name from its canonical resource, including old inventories.
func (e Entry) Label() string {
	ref := e.Reference()
	resource := ref.Metadata["resource"]
	if resource == "" {
		_, resource, _ = strings.Cut(ref.ID, ":")
	}

	if _, name, ok := strings.Cut(resource, "/clusters/"); ok && name != "" {
		return name
	}

	return e.ReferenceEntry.Label()
}

type gcloudToken struct {
	ctx     context.Context
	account string
}

// Token uses an existing configured gcloud account without launching an interactive login.
func (s gcloudToken) Token() (*oauth2.Token, error) {
	b, err := exec.CommandContext(s.ctx, "gcloud", "--quiet", "auth", "print-access-token", "--account", s.account).Output()
	if err != nil {
		return nil, fmt.Errorf("obtain existing gcloud credentials for configured account: %w; run gcloud auth login explicitly", err)
	}

	if strings.TrimSpace(string(b)) == "" {
		return nil, errors.New("gcloud returned an empty access token")
	}

	return &oauth2.Token{AccessToken: strings.TrimSpace(string(b)), Expiry: time.Now().Add(5 * time.Minute)}, nil
}

// options chooses explicit credentials when configured, otherwise preserving ADC.
func (p *GKE) options(ctx context.Context) ([]option.ClientOption, error) {
	if len(p.ClientOptions) > 0 {
		return p.ClientOptions, nil
	}

	a := p.Store.Config.Authentication
	if a != nil && a.Type == "service-account" {
		if a.ServiceAccountFilePath == "" {
			return nil, errors.New("service-account authentication requires serviceAccountFilePath")
		}

		path, err := config.Expand(a.ServiceAccountFilePath)
		if err != nil {
			return nil, err
		}

		creds, err := credentials.NewCredentialsFromFile(credentials.ServiceAccount, path, &credentials.DetectOptions{Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}})
		if err != nil {
			return nil, err
		}

		return []option.ClientOption{option.WithAuthCredentials(creds)}, nil
	}

	if p.Store.Config.GCPAccount != "" {
		return []option.ClientOption{option.WithTokenSource(oauth2.ReuseTokenSource(nil, gcloudToken{ctx, p.Store.Config.GCPAccount}))}, nil
	}

	return nil, nil
}

// Discover searches visible active projects when needed, then lists all cluster
// locations in every project. Projects that fail or report missing zones are skipped.
func (p *GKE) Discover(ctx context.Context) ([]state.Reference, error) {
	options, err := p.options(ctx)
	if err != nil {
		return nil, err
	}

	client, err := container.NewClusterManagerClient(ctx, options...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	projects := p.Store.Config.ProjectIDs
	if len(projects) == 0 {
		rm, err := resourcemanager.NewProjectsClient(ctx, options...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rm.Close() }()

		it := rm.SearchProjects(ctx, &resourcepb.SearchProjectsRequest{Query: "state:ACTIVE"})
		for {
			project, err := it.Next()
			if errors.Is(err, iterator.Done) {
				break
			}

			if err != nil {
				return nil, err
			}

			projects = append(projects, project.ProjectId)
		}
	}

	var refs []state.Reference
	var projectErrors []error
	log := p.Log
	if log == nil {
		log = slog.Default()
	}

	for _, project := range projects {
		projectRefs, err := p.discoverProject(ctx, client, project)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if err != nil {
			log.WarnContext(ctx, "skipping GKE project during discovery", "store", p.Store.Key(), "project", project, "error", err)
			projectErrors = append(projectErrors, fmt.Errorf("project %s: %w", project, err))
			continue
		}

		refs = append(refs, projectRefs...)
	}

	if len(projectErrors) > 0 && len(projectErrors) == len(projects) {
		return nil, fmt.Errorf("all GKE projects failed discovery: %w", errors.Join(projectErrors...))
	}

	return refs, nil
}

func (p *GKE) discoverProject(ctx context.Context, client *container.ClusterManagerClient, project string) ([]state.Reference, error) {
	result, err := client.ListClusters(ctx, &pb.ListClustersRequest{Parent: "projects/" + project + "/locations/-"})
	if err != nil {
		return nil, err
	}

	if len(result.MissingZones) != 0 {
		return nil, fmt.Errorf("missing zones %s", strings.Join(result.MissingZones, ", "))
	}

	var refs []state.Reference
	for _, cluster := range result.Clusters {
		location := cluster.GetLocation()
		if location == "" {
			return nil, errors.New("GKE returned a cluster without a location")
		}

		endpoint, ca, err := clusterConnection(cluster, p.Store.Config.PreferredEndpoint)
		if err != nil {
			return nil, fmt.Errorf("cluster %s: %w", cluster.GetName(), err)
		}

		id := "projects/" + project + "/locations/" + location + "/clusters/" + cluster.GetName()
		r := shared.Reference(p.Store, id, "gke_"+project+"_"+location+"_"+cluster.GetName(), map[string]string{
			"resource": id, "endpoint": endpoint, "ca": ca,
		})
		// Search labels stay qualified; isolated kubeconfigs use the cluster name.
		r.Context = cluster.GetName()
		refs = append(refs, r)
	}

	return refs, nil
}

// gkeEndpoint selects only an available requested endpoint, not a silent fallback
// to a differently exposed control plane.
func gkeEndpoint(c *pb.Cluster, preference string) (string, error) {
	endpoint := c.GetEndpoint()
	ip := c.GetControlPlaneEndpointsConfig().GetIpEndpointsConfig()

	switch preference {
	case "dns":
		endpoint = c.GetControlPlaneEndpointsConfig().GetDnsEndpointConfig().GetEndpoint()
	case "private":
		endpoint = ip.GetPrivateEndpoint()
	case "public":
		endpoint = ip.GetPublicEndpoint()
		if endpoint == "" && ip == nil && c.GetPrivateClusterConfig() == nil {
			endpoint = c.GetEndpoint()
		}
	}

	if endpoint == "" {
		return "", fmt.Errorf("GKE endpoint %q is unavailable", preference)
	}

	return "https://" + endpoint, nil
}

// clusterConnection validates the selected endpoint and its public trust settings.
func clusterConnection(c *pb.Cluster, preference string) (string, string, error) {
	endpoint, err := gkeEndpoint(c, preference)
	if err != nil {
		return "", "", err
	}

	ca := c.GetMasterAuth().GetClusterCaCertificate()
	if preference == "dns" {
		ca = ""
	} else if ca == "" {
		return "", "", errors.New("GKE returned no cluster CA for its IP endpoint")
	}

	return endpoint, ca, nil
}

// Materialize uses discovered public connection settings without authenticating
// to Google or running the GKE auth plugin. Older inventories retain a live lookup
// until discovery refreshes them with endpoint and CA metadata.
func (p *GKE) Materialize(ctx context.Context, r state.Reference) ([]byte, error) {
	endpoint, hasEndpoint := r.Metadata["endpoint"]
	ca, hasCA := r.Metadata["ca"]
	if !hasEndpoint || !hasCA {
		options, err := p.options(ctx)
		if err != nil {
			return nil, err
		}

		client, err := container.NewClusterManagerClient(ctx, options...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		c, err := client.GetCluster(ctx, &pb.GetClusterRequest{Name: r.Metadata["resource"]})
		if err != nil {
			return nil, err
		}

		endpoint, ca, err = clusterConnection(c, p.Store.Config.PreferredEndpoint)
		if err != nil {
			return nil, err
		}
	}

	if endpoint == "" || (p.Store.Config.PreferredEndpoint != "dns" && ca == "") {
		return nil, errors.New("GKE inventory has invalid endpoint/CA metadata; run switcher discover")
	}

	credential := &api.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1beta1", Command: "gke-gcloud-auth-plugin",
		InteractiveMode: api.NeverExecInteractiveMode, ProvideClusterInfo: true,
	}

	if p.Store.Config.GCPAccount != "" {
		credential.Env = append(credential.Env, api.ExecEnvVar{Name: "CLOUDSDK_CORE_ACCOUNT", Value: p.Store.Config.GCPAccount})
	} else {
		credential.Args = []string{"--use_application_default_credentials"}
	}

	if a := p.Store.Config.Authentication; a != nil && a.Type == "service-account" {
		path, err := config.Expand(a.ServiceAccountFilePath)
		if err != nil {
			return nil, err
		}

		credential.Args = []string{"--use_application_default_credentials"}
		credential.Env = []api.ExecEnvVar{{Name: "GOOGLE_APPLICATION_CREDENTIALS", Value: path}}
	}

	return kubeconfig.Generate(r.Context, endpoint, ca, credential)
}
