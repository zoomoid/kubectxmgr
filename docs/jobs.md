# Background discovery

Jobs run short-lived `discover --due` commands. There is no persistent application
daemon and no shell-startup discovery process.

## Homebrew

The Homebrew formula registers a discovery job with `brew services`. Start it
with `brew services start kubectxmgr` and manage it with `brew services list`,
`brew services restart kubectxmgr`, or `brew services stop kubectxmgr`. Do not
use `sudo`; the job should use your own configuration and credentials.
Installation alone does not start the job.

The job checks every 15 minutes with `switcher discover --due`, using launchd on
macOS and a systemd user timer on Linux. Store refresh settings still decide when
to scan. The job uses Homebrew's service PATH and does not capture shell
environment variables. Use `switcher jobs install` instead if you need custom
paths, environment capture, or shorter scheduling intervals. Stop and uninstall any
native job with `switcher jobs uninstall` before switching to Homebrew.

## Management

- `jobs template` prints the native files without installing anything.
- `jobs template --output-directory` writes separate native files without
  activation and refuses to overwrite existing files. macOS stdout rendering is
  a standalone plist; Linux stdout includes labeled service/timer sections.
- `jobs install` writes per-user files and enables/starts the job.
- `jobs install --no-enable` writes files without activation.
- `jobs install --force` explicitly permits replacing unrelated files at the
  exact scheduler paths.
- `jobs status` queries the native service manager.
- `jobs uninstall` unloads/disables the owned job before removing its files.
- `--platform linux` or `--platform darwin` permits cross-platform rendering.
  Installation requires the native platform.
- `--executable` selects an absolute, stable installed binary path. Do not install
  a job pointing at a temporary `go run` executable.

No root operation, shell-rc edit, state deletion, or live scheduler registration
occurs merely by generating templates. Reinstallation is idempotent for owned
files. Activation failure is reported separately from successful file writes.

## Linux

Files are installed under `~/.config/systemd/user`:

- `kubectxmgr-discovery.service`: oneshot discovery, private file creation mask.
- `kubectxmgr-discovery.timer`: periodic activation and persistent catch-up.

The user service manager must be available. The tool does not enable lingering
or install a system-wide/root service. Monotonic periodic activation supports
arbitrary refresh durations; a persistent calendar wake-up and startup trigger
cover missed runs. Due checks prevent unnecessary cloud scans.
Sub-minute configured intervals use a second-based calendar schedule.
Logs are available through the user's systemd journal.

## macOS

The user LaunchAgent is installed at
`~/Library/LaunchAgents/io.github.zoomoid.kubectxmgr.discovery.plist`.

It uses `RunAtLoad` and `StartInterval`, with a private creation mask and protected
log files under the selected state directory's `logs/` directory. Installation
uses the logged-in user's `gui/<uid>` launchd domain. A GUI login domain must exist;
headless environments without it receive an explicit native-manager error.
The tool does not install a system LaunchDaemon.

## Scheduling and concurrency

The default wake interval is 15 minutes, reduced to the shortest configured store
refresh interval when needed. Store refresh ages determine whether a job actually
scans. Positive sub-second discovery ages are accepted by configuration but cannot
be installed as OS schedules; job intervals must be at least one second.

Scheduled runs skip stores already being scanned. Full manual discovery waits
cancellably and then performs its requested fresh scan. Atomic per-store
publication ensures readers see a complete old or new snapshot. A process killed
during discovery may leave a `running` attempt record; the next attempt replaces
it, and the prior complete snapshot remains usable.

## Credentials and environment

Job files use absolute executable/config/state/source paths. PATH and filesystem
discovery inputs are captured without inheriting a shell's generated session
kubeconfig. Configuration-relative paths remain stable regardless of job working
directory.

Only a fixed non-secret allowlist is captured from the installation environment:
AWS profile/region/config paths/role identity/web-identity token **path**, GCP ADC
and gcloud config **paths**, and Azure tenant/client identities and credential
**paths**. API keys, access tokens, AWS secret keys, and Azure client secrets are
never embedded. Inspect `jobs template` before installing when account/path
selection matters. Reinstall after changing captured paths or the binary location.

Cloud authentication must already work noninteractively in the job environment.
Environment-only secret credentials must be provisioned externally through the
service manager or replaced by supported credential files/profiles. They are not
copied into job files. Expired AWS SSO, gcloud, Azure CLI, or keychain access
requires explicit reauthentication. Credential helpers must be reachable through
the captured PATH.

An enabled scheduler does not prove that cloud discovery succeeded. Inspect
`discovery status` and native logs for per-store outcomes. Optional-store failures
warn without failing ordinary discovery; they still block execution and dry-run.

Automated tests use temporary homes and fake manager commands. They never enable
jobs in the developer's actual user session. Native activation and live cloud
authentication need validation in the intended user environment.
