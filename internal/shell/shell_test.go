package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellIntegrationPreservesOutputAndStatus(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "switcher fake")
	kubeconfig := filepath.Join(dir, "config, quote' file")
	require.NoError(t, os.WriteFile(kubeconfig, []byte("test"), 0600))
	program := `#!/bin/sh
response="$2"
shift 2
case "$1" in
  select) printf 'set:%s' "$TEST_CONFIG" > "$response" ;;
  unset-context) printf unset > "$response" ;;
  fail) printf failure; exit 7 ;;
  *) printf 'normal output' ;;
esac
`
	require.NoError(t, os.WriteFile(executable, []byte(program), 0700))
	for _, name := range []string{"bash", "zsh", "fish"} {
		t.Run(name, func(t *testing.T) {
			interpreter, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s unavailable", name)
			}
			text, err := Init(name, executable)
			require.NoError(t, err)
			var script string
			if name == "fish" {
				script = text + "\nkubeswitch --regex; echo; kubeswitch select; printf '%s\\n' \"$KUBECONFIG\"; kubeswitch unset-context; set -q KUBECONFIG; and exit 3; kubeswitch fail; exit $status\n"
			} else {
				script = text + "\nswitch --regex; echo; switch select; printf '%s\\n' \"$KUBECONFIG\"; switch unset-context; test -z \"${KUBECONFIG+x}\" || exit 3; switch fail; exit $?\n"
			}
			cmd := exec.Command(interpreter, "-c", script)
			cmd.Env = append(os.Environ(), "TEST_CONFIG="+kubeconfig)
			b, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, "%s", b)
			require.Equal(t, 7, exit.ExitCode(), "%s", b)
			require.Contains(t, string(b), "normal output\n"+kubeconfig+"\nfailure")
		})
	}
}
