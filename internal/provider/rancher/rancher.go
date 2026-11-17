// Package rancher discovers Rancher clusters and manages short-lived kubeconfig issuance.
package rancher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/provider/shared"
	"github.com/zoomoid/kubectxmgr/internal/state"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Rancher uses RK-API inventory and short-lived generated kubeconfigs by default.
// The unsupported v3 API requires explicit legacy configuration.
type Rancher struct {
	Store config.Store
	Disk  state.Disk
	HTTP  *http.Client
}

// connection normalizes the server origin and loads its bearer credential.
func (p *Rancher) connection() (string, string, error) {
	base := strings.TrimRight(p.Store.Config.RancherAPIAddress, "/")
	base = strings.TrimSuffix(base, "/v3")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("invalid Rancher API address")
	}

	localHTTP := u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")
	if u.Scheme != "https" && !localHTTP {
		return "", "", errors.New("Rancher API address must use HTTPS")
	}

	token := p.Store.Config.RancherToken
	if p.Store.Config.RancherTokenFile != "" {
		path, err := config.Expand(p.Store.Config.RancherTokenFile)
		if err != nil {
			return "", "", err
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return "", "", err
		}

		token = strings.TrimSpace(string(b))
	} else if strings.Contains(token, "$") {
		var err error
		token, err = config.Expand(token)
		if err != nil {
			return "", "", err
		}
	}

	if token == "" {
		return "", "", errors.New("Rancher token is empty")
	}

	return base, token, nil
}

// client creates the paginated Kubernetes-style RK-API inventory client.
func (p *Rancher) client() (dynamic.Interface, error) {
	base, token, err := p.connection()
	if err != nil {
		return nil, err
	}

	return dynamic.NewForConfig(&rest.Config{Host: base, BearerToken: token, Timeout: 60 * time.Second})
}

// rancherClusters is the management cluster collection exposed through RK-API.
var rancherClusters = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "clusters"}

// Discover exhausts the configured inventory API without issuing kubeconfig tokens.
// RK-API failures never trigger an implicit downgrade to the legacy API.
func (p *Rancher) Discover(ctx context.Context) ([]state.Reference, error) {
	if p.Store.Config.LegacyAPI {
		return p.legacyDiscover(ctx)
	}

	client, err := p.client()
	if err != nil {
		return nil, err
	}

	var refs []state.Reference
	token := ""
	for {
		list, err := client.Resource(rancherClusters).List(ctx, metav1.ListOptions{Limit: 100, Continue: token})
		if err != nil {
			return nil, fmt.Errorf("Rancher RK-API cluster listing failed (no automatic legacy fallback): %w", err)
		}

		for _, c := range list.Items {
			name, _, err := unstructured.NestedString(c.Object, "spec", "displayName")
			if err != nil {
				return nil, err
			}

			if name == "" {
				name = c.GetName()
			}

			r := shared.Reference(p.Store, c.GetName(), name, map[string]string{"cluster": c.GetName()})
			// RK-API kubeconfigs use the stable cluster ID for currentContext.
			r.Context = c.GetName()
			refs = append(refs, r)
		}

		token = list.GetContinue()
		if token == "" {
			break
		}
	}

	return refs, nil
}

// Materialize serializes credential issuance per reference and reuses an owned
// unexpired cache entry. RK-API credentials request a five-minute TTL; generated
// kubeconfigs are cached for four minutes. Failed creation is not automatically retried.
func (p *Rancher) Materialize(ctx context.Context, r state.Reference) ([]byte, error) {
	disk := p.Disk
	if p.Store.Cache != nil {
		disk.Root = p.Store.Cache.Config.Path
	}

	key := r.ID + p.Store.Fingerprint()
	unlock, err := disk.Lock(ctx, "credential-"+key, true)
	if err != nil {
		return nil, err
	}
	defer unlock()

	path := disk.Path("credentials", key)
	cache, err := state.Read[state.Credentials](path)
	if err == nil {
		if cache.Owner == "kubectxmgr" && cache.Version == state.Version && time.Now().Before(cache.Expires) {
			return cache.Data, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var data []byte
	if p.Store.Config.LegacyAPI {
		data, err = p.legacyMaterialize(ctx, r)
	} else {
		base, _, connectErr := p.connection()
		err = connectErr
		if err == nil {
			var result struct {
				Status struct {
					Value string `json:"value"`
				} `json:"status"`
			}

			// Creation returns the credential value once. Avoid a client that
			// automatically retries this non-idempotent token-issuance request.
			err = p.request(ctx, http.MethodPost, base+"/apis/ext.cattle.io/v1/kubeconfigs", map[string]any{
				"apiVersion": "ext.cattle.io/v1", "kind": "Kubeconfig",
				"spec": map[string]any{"clusters": []any{r.Metadata["cluster"]}, "currentContext": r.Metadata["cluster"], "ttl": int64(300), "description": "kubectxmgr"},
			}, &result)
			data = []byte(result.Status.Value)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("Rancher credential creation failed; request was not retried (check server before retrying): %w", err)
	}

	if len(data) == 0 {
		return nil, errors.New("Rancher returned no one-time kubeconfig value")
	}

	if err := state.Atomic(path, state.Credentials{Owner: "kubectxmgr", Key: key, Version: state.Version, Expires: time.Now().Add(4 * time.Minute), Data: data}); err != nil {
		return nil, err
	}

	return data, nil
}

type legacyCluster struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Actions map[string]string `json:"actions"`
}

// request sends a single same-origin API request without an application retry loop.
// The default transport refuses redirects so bearer credentials stay at the origin.
func (p *Rancher) request(ctx context.Context, method, address string, body any, out any) error {
	base, token, err := p.connection()
	if err != nil {
		return err
	}

	target, err := url.Parse(address)
	if err != nil {
		return err
	}

	origin, err := url.Parse(base)
	if err != nil {
		return err
	}

	target = origin.ResolveReference(target)
	if target.Scheme != origin.Scheme || target.Host != origin.Host {
		return errors.New("Rancher returned a cross-origin API link")
	}

	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("Rancher API redirect refused") }}
	}

	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Rancher API returned HTTP %d", response.StatusCode)
	}

	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(out)
}

// legacyDiscover follows explicit v3 pagination while rejecting pagination loops.
func (p *Rancher) legacyDiscover(ctx context.Context) ([]state.Reference, error) {
	base, _, err := p.connection()
	if err != nil {
		return nil, err
	}

	next := base + "/v3/clusters?limit=100"
	visited := map[string]bool{}
	var refs []state.Reference
	for next != "" {
		if visited[next] {
			return nil, errors.New("Rancher pagination loop")
		}

		visited[next] = true
		var page struct {
			Data       []legacyCluster `json:"data"`
			Pagination struct {
				Next string `json:"next"`
			} `json:"pagination"`
		}

		if err := p.request(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, err
		}

		for _, c := range page.Data {
			if c.ID == "" {
				return nil, errors.New("Rancher cluster has no ID")
			}

			if c.Name == "" {
				c.Name = c.ID
			}

			r := shared.Reference(p.Store, c.ID, c.Name, map[string]string{"cluster": c.ID})
			r.Context = c.Name
			refs = append(refs, r)
		}

		next = page.Pagination.Next
	}

	return refs, nil
}

// legacyMaterialize follows the configured v3 generateKubeconfig action once.
func (p *Rancher) legacyMaterialize(ctx context.Context, r state.Reference) ([]byte, error) {
	base, _, err := p.connection()
	if err != nil {
		return nil, err
	}

	var cluster legacyCluster
	if err := p.request(ctx, http.MethodGet, base+"/v3/clusters/"+url.PathEscape(r.Metadata["cluster"]), nil, &cluster); err != nil {
		return nil, err
	}

	action := cluster.Actions["generateKubeconfig"]
	if action == "" {
		return nil, errors.New("Rancher cluster has no generateKubeconfig action")
	}

	var result struct {
		Config string `json:"config"`
	}
	if err := p.request(ctx, http.MethodPost, action, map[string]any{}, &result); err != nil {
		return nil, err
	}

	return []byte(result.Config), nil
}
