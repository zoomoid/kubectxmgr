package testutil

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// Binary builds the real CLI for tests that exercise detached subprocesses.
func Binary(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(t.TempDir(), "switcher")
	cmd := exec.Command("go", "build", "-o", path, "./cmd/switcher")
	cmd.Dir = filepath.Join(filepath.Dir(file), "../..")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)

	return path
}
