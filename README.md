# kubectxmgr

A Go Kubernetes context switcher with cached discovery and offline restoration.
The executable is named `switcher`.

## Requirements

- Go 1.27.4 or newer in the Go 1.27 series.
- Linux or macOS.
- Provider credentials and the credential helpers listed in [providers](docs/providers.md).
- golangci-lint v2 for linting.

Build with `make build`, test with `make test`, and lint with `make lint`.
The local executable is `./switcher`. `go install ./cmd/switcher` installs it into
the configured Go binary directory. Release automation is not configured.

## Code layout

- [`cmd/switcher`](cmd/switcher) contains the executable entry point and signal handling.
- [`internal/cmd`](internal/cmd) assembles the root command from individual subcommand
  packages. [`internal/cmd/shared`](internal/cmd/shared) provides flags and lazy runtime
  dependencies so local restoration does not initialize providers.
- [`internal/provider`](internal/provider) defines the provider interface, factory, and
  common credential-cache handling. Implementations and their tests live in separate
  [`aks`](internal/provider/aks), [`eks`](internal/provider/eks),
  [`filesystem`](internal/provider/filesystem), [`gke`](internal/provider/gke), and
  [`rancher`](internal/provider/rancher) packages. A small
  [`shared`](internal/provider/shared) package preserves consistent inventory identities
  without coupling the implementations to the factory.
- Discovery, providers, state, shell integration, and scheduler jobs remain separate
  from command assembly. Shell and scheduler source templates are documented
  package-level variables in their respective packages.

## Shell integration

Source the output of `switcher init bash`, `switcher init zsh`, or
`switcher init fish` from the relevant shell initialization file.

- bash/zsh expose `switch`; fish exposes `kubeswitch` because `switch` is reserved.
- The shell function exports the selected `KUBECONFIG`. The executable cannot
  change its parent shell's environment by itself.
- `switch .` restores the latest successful tool-managed context and namespace.
- Restoration is local-only, even while discovery is blocked or credentials have
  expired. No discovery, authentication helper, or namespace query runs.
- Missing saved state returns an error. Initializing integration does not restore
  a context or edit shell initialization files automatically.
- Each selection/restore creates a private, independent kubeconfig. Source files
  and other shells' kubeconfigs are not changed.
- Session files persist for the shell's lifetime and are not automatically deleted:
  the application cannot reliably determine when another shell has stopped using
  a file. Remove only known-unused session files when reclaiming disk space.

## Commands

| Command | Behavior |
| --- | --- |
| `switcher` | Search complete cached store snapshots; allow first interactive discovery if none exist |
| `switcher <context>` / `set-context` | Select an unambiguous context name or unique inventory ID |
| `switcher .` / `restore` | Restore the global last context and namespace from local state |
| `switcher list-contexts [pattern]` | Print cached inventory as JSON; never discover implicitly |
| `switcher current-context` | Print the active kubeconfig's current context |
| `switch unset-context` | Unset this shell's `KUBECONFIG` |
| `switcher ns [namespace]` | Query namespaces live, or set a namespace explicitly without list permission |
| `switcher discover` | Force discovery of all configured store scopes |
| `switcher discover --due` | Refresh only due/missing inventories; skip busy stores |
| `switcher discovery status` | Print per-store inventory and last-attempt state as JSON |
| `switcher exec <pattern> -- <command> [args...]` | Fresh complete discovery, then sequential execution |
| `switcher exec --dry-run [--json] <pattern> -- <command> [args...]` | Print targets and exact argv; do not generate credentials or run the command |
| `switcher jobs template/install/uninstall/status` | Manage per-user background discovery |
| `switcher cache clean` | Remove owned credential-cache entries; keep last-selection and session state |
| `switcher completion <shell>` | Generate command completion |

Use the shell function instead of the executable for commands that must update the
current shell environment. Direct executable selection prints the resulting path.
`--machine` prints only that path. `--help` lists flags for each command.

Fuzzy search is the TUI default. Tab toggles fuzzy and RE2-compatible regex
matching; `--regex` starts in regex mode. The TUI supports arrow keys, Ctrl-J/K,
Enter, Escape, and Ctrl-C. It has no kubeconfig preview pane. Execution and
noninteractive listing use glob patterns by default; `*` matches across `/`.
`--regex` selects regex matching explicitly. Quote patterns in the calling shell.

## Discovery guarantees

- Each store publishes a snapshot only after all of its configured scopes,
  pages, and required detail requests succeed. An empty complete scan is valid.
- A failed store retains its last complete snapshot. Other successful stores
  publish independently. This is not a globally transactional cloud inventory.
- Cached selection is immediately available and reports stale/missing/failed
  stores. Cloud credentials may be fetched after choosing a remote context.
- `required:false` makes ordinary discovery failure a warning. It never allows
  incomplete results to replace a store snapshot.
- Both execution and dry-run require a fresh, complete scan of **every**
  configured store, including optional stores. No command starts on partial discovery.
- “Complete” covers configured scopes visible to the authenticated identity,
  not inaccessible accounts, projects, subscriptions, regions, or clusters.
- Concurrent processes use per-store advisory locks and atomic file replacement.
  Readers do not wait for discovery locks. Explicit discovery waits cancellably;
  scheduled discovery skips busy stores.

Execution preserves argv without an implicit shell. If legacy `execShell` is set,
exactly one script argument must follow `--`. Execution stops on the first failure,
propagates nonzero child status, and leaves global last-selection state unchanged.
Dry-run may authenticate to discovery APIs and publish complete inventories, but
does not create kubeconfigs, issue Rancher tokens, invoke Kubernetes authentication
plugins, execute the appended command, or change the saved selection.

## Configuration and state

See [configuration](docs/configuration.md), [providers](docs/providers.md), and
[background jobs](docs/jobs.md).

Defaults:

- Configuration: `~/.kube/switch-config.yaml`.
- State: `~/.kube/kubectxmgr`.
- Refresh age: 15 minutes.
- Discovery concurrency: four stores; each provider scans its pages/scopes
  sequentially to bound API pressure.
- Store timeout: two minutes.
- Namespace listing timeout: 15 seconds; no persistent namespace cache.

State includes discovery metadata, credential caches, and the saved kubeconfig.
New directories use `0700`; state files use `0600`. Existing shared scheduler
directories are not chmodded. Credential files are **not encrypted**.
Protect the state directory and any configured credential-cache directory.
Expired saved credentials are not renewed by restore; reselect the context
explicitly to obtain fresh credentials.

## Compatibility

The supported-provider `SwitchConfig` format is retained; the legacy CLI/shell
response protocol and old indexes/history are not. Existing config files are never
rewritten. Alias commands, history navigation, preview panes, hooks, plugins,
unsupported providers, and source-context deletion are not implemented.

Cloud integrations are tested with SDK-backed fake APIs, not certified against
live cloud accounts. Tests cover failed pages, partial results, atomic publication,
cross-process locking, namespace restoration, dry-run, and scheduler lifecycle
operations without installing jobs in the developer's user session.
