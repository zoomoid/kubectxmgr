// Package eks discovers Amazon EKS clusters and builds exec-authenticated kubeconfigs.
package eks

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
	"github.com/zoomoid/kubectxmgr/internal/provider/shared"
	"github.com/zoomoid/kubectxmgr/internal/state"
	api "k8s.io/client-go/tools/clientcmd/api"
)

// EKS discovers clusters in one resolved AWS profile/region.
type EKS struct {
	Store         config.Store
	ClientFactory func(context.Context) (*eks.Client, aws.Config, error)
}

// client keeps the AWS SDK credential chain, including shared config and SSO support.
func (p *EKS) client(ctx context.Context) (*eks.Client, aws.Config, error) {
	if p.ClientFactory != nil {
		return p.ClientFactory(ctx)
	}

	var options []func(*awsconfig.LoadOptions) error
	if p.Store.Config.Profile != "" {
		options = append(options, awsconfig.WithSharedConfigProfile(p.Store.Config.Profile))
	}

	if p.Store.Config.Region != "" {
		options = append(options, awsconfig.WithRegion(p.Store.Config.Region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, cfg, err
	}

	if cfg.Region == "" {
		return nil, cfg, errors.New("EKS region is unset; configure region or AWS_REGION")
	}

	return eks.NewFromConfig(cfg), cfg, nil
}

// Discover exhausts cluster-list pages and resolves stable ARNs for every cluster.
// A failed detail request makes the whole store incomplete.
func (p *EKS) Discover(ctx context.Context) ([]state.Reference, error) {
	client, cfg, err := p.client(ctx)
	if err != nil {
		return nil, err
	}

	var refs []state.Reference
	pager := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, name := range page.Clusters {
			info, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
			if err != nil {
				return nil, err
			}

			if info.Cluster == nil || info.Cluster.Arn == nil {
				return nil, errors.New("EKS returned a cluster without ARN")
			}

			r := shared.Reference(p.Store, *info.Cluster.Arn, name, map[string]string{"name": name, "region": cfg.Region})
			refs = append(refs, r)
		}
	}

	return refs, nil
}

// Materialize refreshes endpoint/CA metadata and configures aws eks get-token
// without executing the Kubernetes authentication helper.
func (p *EKS) Materialize(ctx context.Context, r state.Reference) ([]byte, error) {
	client, _, err := p.client(ctx)
	if err != nil {
		return nil, err
	}

	info, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(r.Metadata["name"])})
	if err != nil {
		return nil, err
	}

	c := info.Cluster
	if c == nil || c.Endpoint == nil || c.CertificateAuthority == nil || c.CertificateAuthority.Data == nil || *c.CertificateAuthority.Data == "" {
		return nil, errors.New("EKS cluster endpoint/CA is unavailable")
	}

	args := []string{"eks", "get-token", "--cluster-name", r.Metadata["name"], "--region", r.Metadata["region"]}
	if p.Store.Config.Profile != "" {
		args = append(args, "--profile", p.Store.Config.Profile)
	}

	return kubeconfig.Generate(r.Context, *c.Endpoint, *c.CertificateAuthority.Data, &api.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1beta1", Command: "aws", Args: args, InteractiveMode: api.NeverExecInteractiveMode,
	})
}
