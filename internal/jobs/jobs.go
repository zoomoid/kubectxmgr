// Package jobs renders and manages user-level systemd and launchd discovery jobs.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zoomoid/kubectxmgr/internal/state"
)

// Label identifies the per-user launchd agent.
const Label = "io.github.zoomoid.kubectxmgr.discovery"

const unit = "kubectxmgr-discovery"
const owner = "Managed by kubectxmgr"

// EnvironmentKeys lists non-secret identity and credential-path settings that
// may be captured in job files. Credential contents must never enter this list.
var EnvironmentKeys = []string{
	"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE",
	"AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_EC2_METADATA_DISABLED",
	"GOOGLE_APPLICATION_CREDENTIALS", "CLOUDSDK_CONFIG", "AZURE_CONFIG_DIR", "AZURE_TENANT_ID", "AZURE_CLIENT_ID",
	"AZURE_CLIENT_CERTIFICATE_PATH", "AZURE_FEDERATED_TOKEN_FILE",
}

// Options supplies resolved paths, scheduler identity, and discovery scheduling.
type Options struct {
	Platform, Home, Executable, Config, State, Path, Kubeconfig string
	ExtraPath                                                   string
	Interval                                                    time.Duration
	UID                                                         int
	Environment                                                 map[string]string
}

// File is one native scheduler definition and its intended installation path.
type File struct{ Path, Content string }

// Runner invokes a native service manager and returns its combined output.
type Runner func(context.Context, string, ...string) ([]byte, error)

// Run invokes a service-manager command with a bounded wait and contextual errors.
func Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return b, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(b)))
	}

	return b, nil
}

// quote escapes a literal systemd value, including percent specifiers.
func quote(s string) string { return strconv.Quote(strings.ReplaceAll(s, "%", "%%")) }

// arg additionally disables ExecStart environment substitution for a literal argv item.
func arg(s string) string { return quote(strings.ReplaceAll(s, "$", "$$")) }

// Templates renders native scheduler files without writing them or contacting a
// service manager. Only the non-secret environment allowlist may be embedded.
func Templates(o Options) ([]File, error) {
	paths := []string{o.Home, o.Executable, o.Config, o.State}
	if o.ExtraPath != "" {
		paths = append(paths, o.ExtraPath)
	}

	for _, path := range paths {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return nil, errors.New("job paths must be absolute and contain no control characters")
		}
	}

	if strings.ContainsAny(o.Path+o.Kubeconfig, "\x00\r\n") {
		return nil, errors.New("job environment contains control characters")
	}

	if o.Interval < time.Second {
		return nil, errors.New("job interval must be at least one second")
	}

	seconds := int64((o.Interval + time.Second - 1) / time.Second)
	keys := make([]string, 0, len(o.Environment))
	for key, value := range o.Environment {
		allowed := false
		for _, name := range EnvironmentKeys {
			if name == key {
				allowed = true
			}
		}

		if !allowed {
			return nil, fmt.Errorf("job environment key %s is not in the non-secret allowlist", key)
		}

		if strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("job environment value for %s contains control characters", key)
		}

		keys = append(keys, key)
	}

	sort.Strings(keys)

	switch o.Platform {
	case "linux":
		dir := filepath.Join(o.Home, ".config", "systemd", "user")
		arguments := []string{arg(o.Executable), "--config-path", arg(o.Config), "--state-directory", arg(o.State)}
		if o.ExtraPath != "" {
			arguments = append(arguments, "--kubeconfig-path", arg(o.ExtraPath))
		}

		execLine := strings.Join(append(arguments, "discover", "--due"), " ")
		service := fmt.Sprintf(systemdServiceTemplate, owner, quote("PATH="+o.Path), quote("KUBECONFIG="+o.Kubeconfig), execLine)
		for _, key := range keys {
			service += fmt.Sprintf(systemdEnvironmentTemplate, quote(key+"="+o.Environment[key]))
		}

		timer := fmt.Sprintf(systemdCalendarTimerTemplate, owner, seconds, unit)
		if seconds >= 60 {
			timer = fmt.Sprintf(systemdIntervalTimerTemplate, owner, seconds, unit)
		}

		return []File{{filepath.Join(dir, unit+".service"), service}, {filepath.Join(dir, unit+".timer"), timer}}, nil
	case "darwin":
		values := []string{o.Executable, "--config-path", o.Config, "--state-directory", o.State}
		if o.ExtraPath != "" {
			values = append(values, "--kubeconfig-path", o.ExtraPath)
		}

		values = append(values, "discover", "--due")
		var args strings.Builder
		for _, v := range values {
			fmt.Fprintf(&args, launchArgumentTemplate, html.EscapeString(v))
		}

		var environment strings.Builder
		for _, key := range keys {
			fmt.Fprintf(&environment, launchEnvironmentTemplate, key, html.EscapeString(o.Environment[key]))
		}

		logDir := filepath.Join(o.State, "logs")
		xml := fmt.Sprintf(launchAgentTemplate, owner, Label, args.String(), seconds, html.EscapeString(o.Path), html.EscapeString(o.Kubeconfig), environment.String(), html.EscapeString(filepath.Join(logDir, "discovery.log")), html.EscapeString(filepath.Join(logDir, "discovery-error.log")))

		return []File{{filepath.Join(o.Home, "Library", "LaunchAgents", Label+".plist"), xml}}, nil
	default:
		return nil, fmt.Errorf("unsupported job platform %q", o.Platform)
	}
}

// owned refuses symlinks/non-regular files before checking the ownership marker.
func owned(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("refusing non-regular job file %s", path)
	}

	b, err := os.ReadFile(path)
	return strings.Contains(string(b), owner), err
}

// Install writes owned job files, then optionally enables and verifies the native
// scheduler. force allows replacement of unrelated regular files at these paths.
func Install(ctx context.Context, o Options, enable, force bool, run Runner) error {
	files, err := Templates(o)
	if err != nil {
		return err
	}

	// Validate every destination before replacing any existing job definition.
	for _, f := range files {
		ours, err := owned(f.Path)
		if err != nil {
			return err
		}

		if _, err := os.Stat(f.Path); err == nil && !ours && !force {
			return fmt.Errorf("refusing unrelated job file %s; use --force to replace it", f.Path)
		}
	}

	if o.Platform == "darwin" {
		if err := state.PrivateDir(filepath.Join(o.State, "logs")); err != nil {
			return err
		}

		if enable {
			loaded, err := launchLoaded(ctx, o, run)
			if err != nil {
				return err
			}

			if loaded {
				if _, err := run(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d/%s", o.UID, Label)); err != nil {
					return fmt.Errorf("unload previous job: %w", err)
				}
			}
		}
	}

	for _, f := range files {
		if err := state.Write(f.Path, []byte(f.Content)); err != nil {
			return err
		}
	}

	if !enable {
		return nil
	}

	if o.Platform == "linux" {
		if _, err := run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return fmt.Errorf("files written, reload failed: %w", err)
		}

		if _, err := run(ctx, "systemctl", "--user", "enable", "--now", unit+".timer"); err != nil {
			return fmt.Errorf("files written, activation failed: %w", err)
		}
	} else {
		if _, err := run(ctx, "launchctl", "enable", fmt.Sprintf("gui/%d/%s", o.UID, Label)); err != nil {
			return err
		}

		if _, err := run(ctx, "launchctl", "bootstrap", fmt.Sprintf("gui/%d", o.UID), files[0].Path); err != nil {
			return fmt.Errorf("files written, bootstrap failed: %w", err)
		}
	}

	_, err = Status(ctx, o, run)
	return err
}

// Status returns native scheduler state; it does not assert cloud discovery succeeded.
func Status(ctx context.Context, o Options, run Runner) ([]byte, error) {
	if o.Platform == "linux" {
		if _, err := run(ctx, "systemctl", "--user", "is-enabled", unit+".timer"); err != nil {
			return nil, err
		}

		return run(ctx, "systemctl", "--user", "status", unit+".timer", "--no-pager")
	}

	if o.Platform == "darwin" {
		return run(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", o.UID, Label))
	}

	return nil, errors.New("unsupported job platform")
}

// Uninstall unloads or disables owned jobs before removing their exact files.
// Discovery snapshots, credential caches, and the saved selection are preserved.
func Uninstall(ctx context.Context, o Options, run Runner) error {
	files, err := Templates(o)
	if err != nil {
		return err
	}

	found := false
	for _, f := range files {
		ours, err := owned(f.Path)
		if err != nil {
			return err
		}

		if !ours {
			if _, err := os.Lstat(f.Path); err == nil {
				return fmt.Errorf("refusing unrelated job file %s", f.Path)
			}
		} else {
			found = true
		}
	}

	if !found {
		return errors.New("no installed job files")
	}

	if o.Platform == "linux" {
		if _, err := run(ctx, "systemctl", "--user", "disable", "--now", unit+".timer"); err != nil {
			return err
		}
	} else {
		loaded, err := launchLoaded(ctx, o, run)
		if err != nil {
			return err
		}

		if loaded {
			if _, err := run(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d/%s", o.UID, Label)); err != nil {
				return err
			}
		}
	}

	for _, f := range files {
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	if o.Platform == "linux" {
		_, err = run(ctx, "systemctl", "--user", "daemon-reload")
	}

	return err
}

// launchLoaded treats only launchctl's explicit "service absent" response as
// absence. Permission and service-manager failures must remain visible to callers.
func launchLoaded(ctx context.Context, o Options, run Runner) (bool, error) {
	b, err := run(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", o.UID, Label))
	if err == nil {
		return true, nil
	}

	if strings.Contains(string(b), "Could not find service") || strings.Contains(string(b), "Could not find specified service") {
		return false, nil
	}

	return false, fmt.Errorf("query existing launchd job: %w", err)
}
