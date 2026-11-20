// Package rancher discovers Rancher clusters and manages kubeconfig issuance.
package rancher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
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
	"k8s.io/client-go/tools/clientcmd"
)

// Rancher uses RK-API inventory and generated kubeconfigs by default.
// The unsupported v3 API requires explicit legacy configuration.
type Rancher struct {
	Store config.Store
	Disk  state.Disk
	HTTP  *http.Client
}

// Entry presents Rancher's display name while retaining its internal cluster ID.
type Entry struct {
	shared.ReferenceEntry
}

// Label uses the discovered name, even when an older context still holds a cluster ID.
func (e Entry) Label() string {
	ref := e.Reference()
	if name := strings.TrimPrefix(ref.Name, ref.Store+"/"); name != "" {
		return name
	}

	return e.ReferenceEntry.Label()
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
// unexpired cache entry. Failed creation is not automatically retried.
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
	var expires time.Time
	if p.Store.Config.LegacyAPI {
		data, err = p.legacyMaterialize(ctx, r)
		expires = time.Now().Add(4 * time.Minute)
	} else {
		base, _, connectErr := p.connection()
		err = connectErr
		if err == nil {
			var result struct {
				Spec struct {
					TTL int64 `json:"ttl"`
				} `json:"spec"`
				Status struct {
					Value string `json:"value"`
				} `json:"status"`
			}

			// Creation returns the credential value once. Avoid a client that
			// automatically retries this non-idempotent token-issuance request.
			started := time.Now()
			err = p.request(ctx, http.MethodPost, base+"/apis/ext.cattle.io/v1/kubeconfigs", map[string]any{
				"apiVersion": "ext.cattle.io/v1", "kind": "Kubeconfig",
				"spec": map[string]any{"clusters": []string{r.Metadata["cluster"]}, "currentContext": r.Metadata["cluster"], "description": "kubectxmgr"},
			}, &result)
			data = []byte(result.Status.Value)
			if ttl := result.Spec.TTL; ttl > 0 && ttl <= math.MaxInt64/int64(time.Second) {
				lifetime := time.Duration(ttl) * time.Second
				// Count from before issuance, with margin for use after selection.
				expires = started.Add(lifetime - min(time.Minute, lifetime/10))
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("Rancher credential creation failed; request was not retried (check server before retrying): %w", err)
	}

	if len(data) == 0 {
		return nil, errors.New("Rancher returned no one-time kubeconfig value")
	}

	if p.Store.Config.AuthorizedClusterEndpoint {
		data, err = selectAuthorizedClusterEndpoint(data, r.Metadata["cluster"])
		if err != nil {
			return nil, err
		}
	}

	if time.Now().Before(expires) {
		if err := state.Atomic(path, state.Credentials{Owner: "kubectxmgr", Key: key, Version: state.Version, Expires: expires, Data: data}); err != nil {
			return nil, err
		}
	}

	return data, nil
}

func selectAuthorizedClusterEndpoint(data []byte, clusterID string) ([]byte, error) {
	cfg, err := clientcmd.Load(data)
	if err != nil {
		return nil, err
	}

	// ACE contexts are named after the target's proxy context, with a suffix
	// for its FQDN or node. An RK-API config also includes Rancher's own API context.
	var proxyNames []string
	for name, ctx := range cfg.Contexts {
		if ctx == nil || cfg.Clusters[ctx.Cluster] == nil {
			continue
		}
		server, err := url.Parse(cfg.Clusters[ctx.Cluster].Server)
		if err == nil && strings.TrimRight(server.Path, "/") == "/k8s/clusters/"+clusterID {
			proxyNames = append(proxyNames, name)
		}
	}

	var direct []string
	for name, ctx := range cfg.Contexts {
		if ctx == nil || cfg.Clusters[ctx.Cluster] == nil || cfg.AuthInfos[ctx.AuthInfo] == nil {
			continue
		}
		if !slices.ContainsFunc(proxyNames, func(proxy string) bool { return strings.HasPrefix(name, proxy+"-") }) {
			continue
		}
		server, err := url.Parse(cfg.Clusters[ctx.Cluster].Server)
		if err != nil || server.Scheme != "https" || server.Hostname() == "" || server.User != nil ||
			server.RawQuery != "" || server.Fragment != "" || (server.Path != "" && server.Path != "/") {
			continue
		}
		direct = append(direct, name)
	}

	if len(direct) == 0 {
		return nil, errors.New("Rancher kubeconfig has no authorized cluster endpoint; enable it on the downstream cluster")
	}

	// RK-API sets the direct context by default; legacy responses can select the proxy.
	if !slices.Contains(direct, cfg.CurrentContext) {
		slices.Sort(direct)
		cfg.CurrentContext = direct[0]
	}

	return clientcmd.Write(*cfg)
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
