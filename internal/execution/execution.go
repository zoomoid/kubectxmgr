// Package execution builds dry-run plans and runs commands against isolated contexts.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/zoomoid/kubectxmgr/internal/kubeconfig"
	"github.com/zoomoid/kubectxmgr/internal/logging"
	"github.com/zoomoid/kubectxmgr/internal/provider"
	"github.com/zoomoid/kubectxmgr/internal/search"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// Target is one planned invocation, shared by dry-run rendering and real execution.
type Target struct {
	Reference state.Reference `json:"target"`
	Namespace string          `json:"namespace"`
	Argv      []string        `json:"argv"`
}

// ExitError preserves a child command's nonzero exit status and failing target.
type ExitError struct {
	Code   int
	Target string
}

// Error identifies the failed context without exposing its credentials.
func (e *ExitError) Error() string {
	return fmt.Sprintf("command failed on %s (exit %d)", e.Target, e.Code)
}

// Validate checks matching and argv rules without discovery or credential retrieval.
func Validate(pattern, mode, shell string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("a command after -- is required")
	}

	if shell != "" && len(argv) != 1 {
		return errors.New("execShell requires exactly one script argument after --")
	}

	_, err := search.Match(nil, pattern, mode)
	return err
}

// Plan matches discovered contexts and copies argv into per-target execution plans.
// A configured shell wraps exactly one script; ordinary argv never uses a shell.
func Plan(refs []state.Reference, pattern, mode, shell string, argv []string) ([]Target, error) {
	if err := Validate(pattern, mode, shell, argv); err != nil {
		return nil, err
	}

	if shell != "" {
		argv = []string{shell, "-c", argv[0]}
	}

	matches, err := search.Match(refs, pattern, mode)
	if err != nil {
		return nil, err
	}

	if len(matches) == 0 {
		return nil, errors.New("no contexts match the pattern")
	}

	targets := make([]Target, 0, len(matches))
	for _, r := range matches {
		ns := r.Namespace
		if ns == "" {
			ns = "<provider/default>"
		}

		targets = append(targets, Target{Reference: r, Namespace: ns, Argv: append([]string(nil), argv...)})
	}

	return targets, nil
}

// Quote renders one argument in POSIX single-quote syntax for human inspection.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

// planLineTemplate shows one invocation without claiming a kubeconfig was created.
var planLineTemplate = "%s namespace=%s KUBECONFIG=<isolated:%s> %s\n"

// targetLineTemplate separates each cluster's command output on stderr.
var targetLineTemplate = "# %s [%s]: %s"

// Print renders a plan as JSON or readable quoted argv without fetching credentials.
func Print(w io.Writer, targets []Target, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(w).Encode(targets)
	}

	for _, t := range targets {
		argv := make([]string, len(t.Argv))
		for i, arg := range t.Argv {
			argv[i] = Quote(arg)
		}

		if _, err := fmt.Fprintf(w, planLineTemplate, t.Reference.ID, t.Namespace, t.Reference.Context, strings.Join(argv, " ")); err != nil {
			return err
		}
	}

	return nil
}

// Environment replaces every inherited KUBECONFIG assignment with the isolated path.
func Environment(base []string, path string) []string {
	env := make([]string, 0, len(base)+1)
	for _, item := range base {
		if !strings.HasPrefix(item, "KUBECONFIG=") {
			env = append(env, item)
		}
	}

	return append(env, "KUBECONFIG="+path)
}

// Materialize obtains kubeconfig bytes for a target only when execution needs them.
type Materialize func(context.Context, state.Reference) ([]byte, error)

// Run executes targets sequentially and stops at the first failure.
// Per-command kubeconfigs are removed; the global saved selection is never changed.
func Run(ctx context.Context, root string, targets []Target, materialize Materialize, in io.Reader, out, stderr io.Writer) error {
	for _, t := range targets {
		if err := runOne(ctx, root, t, materialize, in, out, stderr); err != nil {
			return err
		}
	}

	return nil
}

// runOne owns a target's temporary kubeconfig and child process-group lifecycle.
func runOne(ctx context.Context, root string, t Target, materialize Materialize, in io.Reader, out, stderr io.Writer) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}

	data, err := materialize(ctx, t.Reference)
	if err != nil {
		return fmt.Errorf("%s: %w", t.Reference.ID, err)
	}

	data, _, err = kubeconfig.Select(data, t.Reference.Context, "")
	if err != nil {
		return err
	}

	path, err := kubeconfig.Session(root, data)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(path) }()

	cmd := exec.CommandContext(ctx, t.Argv[0], t.Argv[1:]...)
	cmd.Env = Environment(os.Environ(), path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
	cleanup := configureProcess(cmd)
	defer func() { err = errors.Join(err, cleanup()) }()

	header := fmt.Sprintf(targetLineTemplate, provider.NewEntry(t.Reference).Label(), t.Reference.ID, strings.Join(t.Argv, " "))
	if _, err := fmt.Fprintln(stderr, logging.Muted(stderr, header)); err != nil {
		return err
	}

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &ExitError{Code: max(1, exit.ExitCode()), Target: t.Reference.ID}
		}

		return fmt.Errorf("%s: %w", t.Reference.ID, err)
	}

	return nil
}
