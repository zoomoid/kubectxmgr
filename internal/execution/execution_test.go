package execution

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
)

func TestPlanPreservesArgumentsAndDryRun(t *testing.T) {
	refs := []state.Reference{{ID: "b", Name: "prod/b", Context: "test"}, {ID: "a", Name: "prod/a", Context: "test"}}
	argv := []string{"printf", "%s", "two words", "$(never-run)", "a'b"}
	targets, err := Plan(refs, "prod/*", "glob", "", argv)
	require.NoError(t, err)
	require.Equal(t, "a", targets[0].Reference.ID)
	require.Equal(t, argv, targets[0].Argv)
	var out bytes.Buffer
	require.NoError(t, Print(&out, targets, true))
	require.Contains(t, out.String(), `"$(never-run)"`)
	_, err = Plan(refs, "*", "glob", "sh", argv)
	require.ErrorContains(t, err, "one script")
	targets, err = Plan(refs, "*", "glob", "sh", []string{"printf '%s' 'two words'"})
	require.NoError(t, err)
	require.Equal(t, []string{"sh", "-c", "printf '%s' 'two words'"}, targets[0].Argv)
}

func TestExecutionExitAndTemporaryCleanup(t *testing.T) {
	root := t.TempDir()
	targets := []Target{{Reference: state.Reference{ID: "one", Context: "test"}, Argv: []string{"sh", "-c", "printf '%s' 'two words'; printf err >&2; exit 7"}}, {Reference: state.Reference{ID: "two", Context: "test"}, Argv: []string{"false"}}}
	calls := 0
	materialize := func(context.Context, state.Reference) ([]byte, error) {
		calls++
		return testutil.Kubeconfig("default"), nil
	}

	var out, stderr bytes.Buffer
	err := Run(t.Context(), root, targets, materialize, nil, &out, &stderr)
	var exit *ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 7, exit.Code)
	require.Equal(t, 1, calls)
	require.Equal(t, "two words", out.String())
	require.Contains(t, stderr.String(), "# test [one]")
	require.Contains(t, stderr.String(), "sh -c printf '%s' 'two words'; printf err >&2; exit 7\n")
	require.Contains(t, stderr.String(), "\nerr")
	require.NotContains(t, stderr.String(), "[two]", "targets after a failure must not be announced")
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	require.Empty(t, entries)
	require.Equal(t, []string{"X=1", "KUBECONFIG=new"}, Environment([]string{"KUBECONFIG=old", "X=1", "KUBECONFIG=other"}, "new"))
}

func TestCancellationCleansProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	root := t.TempDir()
	err := Run(ctx, root, []Target{{Reference: state.Reference{ID: "one", Context: "test"}, Argv: []string{"sh", "-c", "trap '' TERM; while :; do sleep 1; done"}}}, func(context.Context, state.Reference) ([]byte, error) { return testutil.Kubeconfig("default"), nil }, nil, io.Discard, io.Discard)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "%v", err)
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	require.Empty(t, entries)
}
