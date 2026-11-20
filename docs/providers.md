# Providers

Current stable Go SDK modules are pinned in [go.mod](../go.mod). Discovery reads
existing credentials noninteractively. It does not launch a browser or change an
active cloud account.

## Filesystem

Configure store `paths` and, optionally, `kubeconfigName`. Every context in each
matching file is discoverable. Discovery parses metadata without executing
credential plugins. Switching preserves exec authentication and embeds referenced
certificate/key files in an isolated kubeconfig; source files are unchanged.

## GKE

Provider configuration:

| Field | Purpose |
| --- | --- |
| `projectIDs` | Explicit project scopes; omitted means enumerate visible active projects |
| `gcpAccount` | Explicit existing gcloud account for discovery and kubeconfig authentication |
| `authentication.authenticationType` | `gcloud`/ADC by default, or `service-account` |
| `authentication.serviceAccountFilePath` | Required credential file for `service-account` |
| `preferredEndpoint` | `dns`, `private`, or `public`; omitted uses the provider endpoint |

Without `gcpAccount`, discovery uses Application Default Credentials. Prepare ADC
explicitly before using the tool. A configured account instead uses noninteractive
`gcloud auth print-access-token --account` and is bound into the generated
credential helper environment. ADC and the active gcloud account are not assumed
to be identical.

Use current `gcloud` and `gke-gcloud-auth-plugin`. Service-account and default ADC
selections instruct the plugin to use ADC; configured service-account files are
bound through `GOOGLE_APPLICATION_CREDENTIALS`. Combining `gcpAccount` with
service-account authentication is rejected.

Discovery uses Resource Manager v3 and Container v1, enumerates all project pages,
and lists all cluster locations. A nonempty GKE `missingZones` response is
incomplete even when HTTP/gRPC succeeded. Missing preferred endpoints are errors.
DNS endpoints use system TLS roots; IP endpoints use the cluster CA.
Discovery persists the selected endpoint and public CA alongside the resource
identity. Selection builds kubeconfigs locally from that metadata without loading
Google credentials or fetching the cluster again; authentication runs only when a
Kubernetes client invokes the configured plugin. Refresh discovery when endpoints
or CAs change; configured credential-cache lifetimes still apply.
Older inventories without connection metadata
still use a live lookup until refreshed with `switcher discover`.
Selected kubeconfigs use the cluster name alone as their context name, while
inventory IDs and qualified search labels retain project and location information.
The picker derives its bare cluster label from the canonical resource name, so
older cached kubeconfig context prefixes do not appear in the visible label or a
newly selected kubeconfig.

## EKS

Configure `profile` and `region` for each desired scope. Existing AWS profile and
region environment defaults remain supported. There is no implicit scan of all
profiles/accounts/regions.

Discovery uses AWS SDK for Go v2, including its shared config, role, SSO,
credential-process, and environment credential support. Complete discovery
includes every `ListClusters` page and the required `DescribeCluster` requests.
The generated kubeconfig uses `aws eks get-token`; install the current AWS CLI.
Refresh expired SSO credentials explicitly before scheduled discovery.

## AKS

Store kind is `azure` or `aks`. Configure:

| Field | Purpose |
| --- | --- |
| `subscriptionID` | Required subscription scope |
| `resourceGroups` | Optional resource-group restriction |
| `endpoint` | Optional supported Azure resource-manager endpoint |

Supported endpoints are public Azure, US Government, and China. The credential
authority and SDK cloud configuration follow the chosen endpoint. Obsolete or
arbitrary endpoint combinations are rejected rather than routed through public
Azure authentication.

Discovery uses current `azidentity` and `armcontainerservice/v9` with all list
pages. Credentials are obtained noninteractively through the default credential
chain. Configure environment/workload credentials, or sign into the Azure CLI
explicitly. Normal discovery (including scheduled `discover --due`) prefetches
**user**, never admin, kubeconfigs in current exec format. Azure's list response
does not include the CA needed to build these locally. Discovery therefore needs
permission to list cluster user credentials as well as clusters; a prefetch failure
retains the previous complete inventory and is reported in discovery status.

User kubeconfigs are cached automatically for one hour in the private state
directory, or the configured credential-cache directory. Selection reuses valid
entries without initializing Azure credentials or making an ARM request. Missing
or expired entries are fetched on selection; run `switcher discover` after upgrading
to warm existing inventories. Kubeconfigs are never stored in inventory metadata.
Execution discovery and dry-run do not prefetch credentials.
Install current Azure `kubelogin` when required by the cluster; its authentication
runs when a Kubernetes client uses the kubeconfig, not during selection.

## Rancher

Configure `rancherAPIAddress` and either `rancherToken` or `rancherTokenFile`.
Environment references in `rancherToken` are expanded without logging the token.
An existing API URL ending in `/v3` is normalized to the server base URL.
HTTPS is required except for localhost testing. System TLS trust applies.

The default integration uses supported RK-API:

- List `clusters.management.cattle.io/v3`, including all pages.
- Create `kubeconfigs.ext.cattle.io/v1` only when a context is selected.
- Capture the one-time `status.value` from creation immediately.
- Use Rancher's `kubeconfig-default-token-ttl-minutes` setting instead of
  requesting a fixed TTL. Cache generated credentials until shortly before
  the lifetime returned by Rancher; skip the cache if no usable TTL is returned.
- Keep each server's `local` cluster distinct through its store identity.

Selected kubeconfigs use the cluster display name when present, falling back to
the cluster ID only when no display name exists. Credential API requests still use
the stable cluster ID; renaming the local context does not change the target.
The picker uses the discovered display name even when an older cached context
still contains the internal cluster ID.

The public Kubeconfig API requires Rancher 2.12 or newer with the feature enabled
and an active Rancher user permitted to create Kubeconfig resources. Rancher
controls token lifetime; reselect the context to obtain fresh credentials when
they expire. Already exported kubeconfigs are not refreshed in place.

Set `authorizedClusterEndpoint: true` under the Rancher store's `config` to
require an [Authorized Cluster Endpoint](https://ranchermanager.docs.rancher.com/how-to-guides/new-user-guides/manage-clusters/access-clusters/authorized-cluster-endpoint).
Enable ACE on the downstream cluster first. The tool selects a direct context
from the Rancher-generated kubeconfig, preserving its TLS and authentication
settings. If Rancher returns no direct context, selection fails rather than
falling back to the proxy. Leaving the option unset preserves Rancher's choice
of current context. Use a separate store ID if you want both store configurations.

Credential-creating POST requests are not retried automatically, including
ambiguous network failures. Check the server before retrying. Expired server-side
resources/tokens follow Rancher's lifecycle; local cache cleaning does not revoke
them. No credentials/tokens are issued during discovery or dry-run.

For older deployments, `legacyAPI: true` explicitly selects the unsupported
Rancher `/v3` API with a warning. Legacy discovery follows pagination and returned
credential-generation action links. Modern API failures and permission errors
never trigger automatic downgrade. Cross-origin legacy API links are rejected.
Legacy credential caching remains four minutes.
