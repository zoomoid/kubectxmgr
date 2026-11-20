# kubectxmgr

A Kubernetes context switcher for GKE, EKS, AKS, Rancher, and local kubeconfigs.
Inspired by [kubeswitch](https://github.com/danielfoehrkn/kubeswitch), with background
discovery, cached selection, and offline restoration. The executable is `switcher`.

> Vast amounts of the code in this repository are generated with AI. Use at your own risk.

## Setup

- Supports Linux and macOS.
- Install with Homebrew: `brew tap zoomoid/kubectxmgr https://github.com/zoomoid/kubectxmgr.git`
  then `brew install zoomoid/kubectxmgr/kubectxmgr`.
- Build with Go 1.27.4 or newer in the Go 1.27 series: `go install ./cmd/switcher`.
- Configure stores and credentials using the [configuration](docs/configuration.md)
  and [provider](docs/providers.md) guides.
- Source the output of `switcher init bash`, `switcher init zsh`, or
  `switcher init fish` in your shell initialization file.

The shell function is `switch` in bash/zsh and `kubeswitch` in fish. Use it for
context changes: the executable cannot export `KUBECONFIG` into its parent shell.
Use [background jobs](docs/jobs.md) to schedule discovery with systemd or launchd.

## Commands

| Command | Behavior |
| --- | --- |
| `switcher` | Search cached contexts; discover on first interactive use |
| `switcher <context>` / `set-context <context>` | Select by name or qualified ID |
| `switcher .` / `restore` | Restore the last context and namespace offline |
| `switcher namespace [name]` / `ns [name]` | Set a namespace or search cached names |
| `switcher list-contexts [pattern]` | Print cached inventory as JSON |
| `switcher current-context` | Print the active context |
| `switch unset-context` | Unset this shell's `KUBECONFIG` |
| `switcher discover` | Refresh all configured stores |
| `switcher discover --due` | Refresh due/missing inventories; skip busy stores |
| `switcher discovery status` | Print discovery status as JSON |
| `switcher exec <pattern> -- <command> [args...]` | Discover, then execute sequentially |
| `switcher exec --dry-run [--json] <pattern> -- <command> [args...]` | Discover and print planned commands without running them |
| `switcher jobs template/install/uninstall/status` | Manage background discovery |
| `switcher cache clean` | Remove credential-cache entries, not saved sessions |
| `switcher completion <shell>` | Generate completions |

Selection is silent by default. `-v` / `--verbose` shows the context, namespace,
and kubeconfig path; `--machine` prints only the path. Use `--help` for all flags.

### Search

- Fuzzy search by default; Tab toggles RE2 regex, and `--regex` starts in regex mode.
- Arrow keys or Ctrl-J/K navigate; Enter selects; Escape/Ctrl-C cancel.
- Search cluster names or qualified IDs. Results appear above the prompt, and the
  picker leaves no terminal output behind.
- Noninteractive patterns use globs by default. Quote them to prevent shell expansion.

## Caching and sessions

- Discovery publishes only complete per-store snapshots. Failed refreshes retain
  previous data; readers do not wait for discovery locks.
- AKS discovery prefetches user kubeconfigs into a private one-hour cache so valid
  selections need no Azure request. Run `switcher discover` once after upgrading.
- Namespace lists open from cache and refresh in the background. The first uncached
  lookup waits for the API. Explicit namespace assignment needs no list permission.
- Namespaces are ordered by recent use. Reselecting a cluster restores its last
  selected namespace without an API query.
- Shell-wrapper selections create isolated kubeconfigs. Direct `switcher namespace`
  updates the active file, affecting other shells that share it.
- Cloud contexts use friendly cluster names; local contexts keep their source names.
- Restore does not discover clusters or renew credentials. Reselect the context
  when credentials expire.
- Session files are not automatically deleted. Remove only files no longer in use.

Execution and dry-run require fresh, complete discovery of every configured store.
Execution preserves arguments without an implicit shell and stops on the first failure.
It shows a temporary refresh spinner and a cluster/command header on stderr;
`--verbose` also shows discovery logs. Redirected output has no progress or color codes.
Dry-run may authenticate to discovery APIs but does not generate kubeconfigs, run
commands, or change the saved selection.

## Configuration

- Config: `~/.kube/switch-config.yaml`
- State: `~/.kube/kubectxmgr`
- Discovery defaults: 15-minute refresh age, four concurrent stores, two-minute
  timeout per store.

State contains credentials and saved kubeconfigs. Directories use `0700` and files
use `0600`; credentials are **not encrypted**.

See [configuration](docs/configuration.md), [providers](docs/providers.md), and
[background jobs](docs/jobs.md) for details.

## Development

| Target | Job |
| --- | --- |
| `make build` / `make install` | Build locally / install |
| `make test` / `make coverage` | Race-enabled tests / coverage |
| `make fmt` / `make fmt-check` | Format / check formatting |
| `make vet` / `make lint` | Static checks / lint |
| `make check` | All checks, including release configuration |
| `make release-check` / `make snapshot` | Validate / build release archives |

Formatting and linting require golangci-lint v2; releases require GoReleaser v2.
Release targets are Linux and macOS, both amd64 and arm64. Pushing a `v*` tag
publishes a GitHub release. Stable tags also update the Homebrew formula on
`main` in this repository; the release workflow needs write access to that branch.

CLI packages live in [`internal/cmd`](internal/cmd), provider implementations in
[`internal/provider`](internal/provider), and the entry point in
[`cmd/switcher`](cmd/switcher).

## Compatibility

Retains `SwitchConfig` for supported providers without rewriting existing files.
Legacy shell protocols, indexes, aliases, history navigation, preview panes, hooks,
and plugins are not supported.

Cloud integrations are tested with fake APIs, not certified against live accounts.
