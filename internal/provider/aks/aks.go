// Package aks discovers Azure Kubernetes Service clusters and retrieves user kubeconfigs.
package aks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	aks "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/provider/shared"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// AKS discovers managed clusters within one Azure subscription and optional groups.
type AKS struct {
	Store  config.Store
	Client *aks.ManagedClustersClient
}

// client uses matching identity and ARM endpoints for the selected Azure cloud.
func (p *AKS) client() (*aks.ManagedClustersClient, error) {
	if p.Client != nil {
		return p.Client, nil
	}

	c := cloud.AzurePublic
	switch strings.TrimRight(p.Store.Config.Endpoint, "/") {
	case "", "https://management.azure.com":
	case "https://management.usgovcloudapi.net":
		c = cloud.AzureGovernment
	case "https://management.chinacloudapi.cn":
		c = cloud.AzureChina
	default:
		return nil, errors.New("unsupported Azure endpoint; use public, US Government, or China resource-manager endpoint")
	}

	credential, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
		ClientOptions: azcore.ClientOptions{Cloud: c},
	})
	if err != nil {
		return nil, err
	}

	options := &arm.ClientOptions{}
	options.Cloud = c

	return aks.NewManagedClustersClient(p.Store.Config.SubscriptionID, credential, options)
}

// Discover exhausts every selected subscription or resource-group page.
// A page failure rejects the entire store inventory.
func (p *AKS) Discover(ctx context.Context) ([]state.Reference, error) {
	client, err := p.client()
	if err != nil {
		return nil, err
	}

	var refs []state.Reference
	add := func(clusters []*aks.ManagedCluster) error {
		for _, c := range clusters {
			if c.ID == nil || c.Name == nil {
				return errors.New("AKS returned a cluster without resource ID/name")
			}

			id, err := arm.ParseResourceID(*c.ID)
			if err != nil {
				return err
			}

			refs = append(refs, shared.Reference(p.Store, *c.ID, *c.Name, map[string]string{"name": *c.Name, "resourceGroup": id.ResourceGroupName}))
		}

		return nil
	}

	if len(p.Store.Config.ResourceGroups) == 0 {
		pager := client.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}

			if err := add(page.Value); err != nil {
				return nil, err
			}
		}
	} else {
		for _, group := range p.Store.Config.ResourceGroups {
			pager := client.NewListByResourceGroupPager(group, nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}

				if err := add(page.Value); err != nil {
					return nil, err
				}
			}
		}
	}

	return refs, nil
}

// Materialize requests user credentials in exec-plugin format, never admin credentials.
func (p *AKS) Materialize(ctx context.Context, r state.Reference) ([]byte, error) {
	client, err := p.client()
	if err != nil {
		return nil, err
	}

	format := aks.FormatExec
	result, err := client.ListClusterUserCredentials(ctx, r.Metadata["resourceGroup"], r.Metadata["name"], &aks.ManagedClustersClientListClusterUserCredentialsOptions{Format: &format})
	if err != nil {
		return nil, err
	}

	if len(result.Kubeconfigs) == 0 || len(result.Kubeconfigs[0].Value) == 0 {
		return nil, fmt.Errorf("AKS returned no user kubeconfig for %s", r.Name)
	}

	return result.Kubeconfigs[0].Value, nil
}
