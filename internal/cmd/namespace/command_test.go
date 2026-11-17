package namespace

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/namespacecache"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

func TestNamespaceDirectAndWrappedSelection(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "wrapped"}[wrapped], func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source")
			require.NoError(t, os.WriteFile(source, testutil.Kubeconfig("before"), 0600))
			t.Setenv("KUBECONFIG", source)
			options := &shared.Options{StatePath: filepath.Join(dir, "state"), Machine: true}
			if wrapped {
				options.ShellResult = filepath.Join(dir, "response")
			}

			starts := 0
			command := newCommand(options, func(namespacecache.Cache, *api.Config) error {
				starts++
				return nil
			})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs([]string{"development"})
			require.NoError(t, command.ExecuteContext(t.Context()))
			require.Equal(t, 1, starts)

			cfg, err := clientcmd.LoadFromFile(source)
			require.NoError(t, err)
			want := "development"
			if wrapped {
				want = "before"
			}
			require.Equal(t, want, cfg.Contexts["test"].Namespace)

			disk, err := options.Disk()
			require.NoError(t, err)
			last, err := disk.Last()
			require.NoError(t, err)
			require.Equal(t, "development", last.Namespace)
			cache, err := namespacecache.New(disk, cfg)
			require.NoError(t, err)
			snapshot, err := cache.Load()
			require.NoError(t, err)
			require.False(t, snapshot.LastUsed["development"].IsZero())
		})
	}
}
