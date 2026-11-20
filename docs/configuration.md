# Configuration

The loader reads `~/.kube/switch-config.yaml`, or the file selected by
`--config-path`. A missing configuration file is allowed when filesystem sources
are available. An existing configuration must contain one YAML document.

## Top-level fields

| Field | Contract |
| --- | --- |
| `kind` | `SwitchConfig` |
| `version` | `v1alpha1` |
| `kubeconfigStores` | List of configured store instances |
| `kubeconfigName` | Default directory-discovery filename glob, initially `config` |
| `refreshIndexAfter` | Positive Go duration, default `15m` |
| `discovery.concurrency` | Number of concurrent store scans, default 4, range 1–64 |
| `discovery.timeout` | Positive timeout per store, default `2m` |
| `execShell` | Optional shell executable; execution requires one script argument after `--` |
| `showPreview` | Accepted with a warning; has no effect |

Unknown fields are errors. Remove legacy `hooks`, plugin configuration, and
unsupported store entries explicitly rather than relying on silent omission.
Aliases and history are not imported.

## Store fields

| Field | Contract |
| --- | --- |
| `kind` | `filesystem`, `gke`, `eks`, `azure`/`aks`, or `rancher` |
| `id` | Stable instance ID, default `default`; IDs must be unique within a kind |
| `paths` | Filesystem source files/directories |
| `kubeconfigName` | Per-store filename glob override |
| `showPrefix` | Show provider/store prefixes, default true |
| `required` | Required for ordinary discovery exit status, default true |
| `refreshIndexAfter` | Per-store positive-duration override |
| `config` | Provider-specific fields described in [providers](providers.md) |
| `cache.kind` | Optional credential cache; only `filesystem` is supported |
| `cache.config.path` | Root of the private cache; entries are stored under `credentials/` |

Multiple scopes use multiple stores where the provider configuration selects one
scope: AWS profile/region, Azure subscription, or Rancher server. Stable inventory
IDs include the store and provider-native cluster identity. Display names do not
identify clusters uniquely. Use the full ID from `list-contexts` when names collide.

Configured relative paths are resolved relative to the configuration file.
`~` and environment references are expanded; unset referenced variables are errors.
CLI paths and `KUBECONFIG` entries are resolved relative to the invoking directory.
The default `~/.kube/config`, when present, is also included unless already listed.
Tool-generated session files are not treated as filesystem discovery inputs.

An explicit file is parsed regardless of its filename. Directory scans match
`kubeconfigName`, recurse through symlinks, and deduplicate resolved paths/cycles.
Unreadable or missing configured roots and malformed matching files make the
entire filesystem store incomplete. Relative certificate/key paths are resolved
against the source file before flattening the selected session kubeconfig.

## Cache and restore

Discovery snapshots are always enabled. `refreshIndexAfter` controls background
freshness, not foreground discovery. Ordinary selection never refreshes an
existing inventory merely because it is old.

Snapshots are versioned YAML files, keyed by store ID with a configuration
fingerprint. Changed scopes/configuration do not silently reuse incompatible
inventories. Successful empty snapshots are retained. Failed attempts are stored
separately and cannot replace a complete inventory.

Explicit filesystem credential caches for filesystem/GKE/EKS stores last one
hour. AKS user kubeconfigs are cached automatically for one hour and prefetched
during normal discovery; execution discovery and dry-run stay metadata-only.
Configured AKS cache paths override the default private state directory.
Rancher RK-API credentials are cached until shortly before their server-issued
TTL elapses; legacy Rancher credentials are cached for four minutes.
Errors and corrupt caches are reported, not
treated as successful empty data. `cache clean` removes owned cached credentials;
it does not revoke remote credentials or remove the saved selection, namespace
inventories, or namespace recency.

Namespace caches are independent of provider discovery. They are keyed by cluster
connection settings, with a most-recent-use timestamp for each selected namespace.
A cached picker refreshes asynchronously, including after the command exits.
Only complete namespace scans replace inventories; failed refreshes retain stale
names. A missing inventory requires one initial synchronous scan.

The saved selection is one atomically published YAML document containing context,
namespace, and kubeconfig. No append-only history is kept. The latest successful
tool-managed selection wins across shells. Namespace edits made by `kubectl` or
other tools are not tracked.

Restoration only copies this saved local state. Even expired credentials restore
without discovery; subsequent Kubernetes access may fail until explicit reselection.
No automatic login/browser action occurs in scheduled discovery.

## Migration boundaries

- Keep supported-provider configuration; remove unsupported providers and hooks.
- `azure` remains valid; `aks` is a synonym.
- GKE accepts both `authentication` and upstream `Authentication`, but not both
  together. API-key and legacy authentication are rejected.
- `execShell` no longer joins arbitrary argv into shell text. Supply exactly one
  intended script, or remove `execShell` and pass a shell explicitly.
- Old kubeswitch index, cache, alias, and history files are not reused.
- The private shell response protocol changed. Regenerate integration with this
  executable rather than sourcing upstream's wrapper.
- `--no-index`, legacy short flags, source-context deletion, previous-context
  navigation, and every upstream command spelling are not compatibility goals.
