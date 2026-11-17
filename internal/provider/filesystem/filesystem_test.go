package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
)

func TestFilesystemAllContextsAndCycles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(path, testutil.Kubeconfig("saved"), 0600))
	require.NoError(t, os.Symlink(dir, filepath.Join(dir, "cycle")))

	p := &Filesystem{Store: config.Store{Kind: "filesystem", ID: "test", Paths: []string{dir, path}, KubeconfigName: "config"}}
	refs, err := p.Discover(t.Context())
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, "saved", refs[0].Namespace)

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	_, err = p.Materialize(t.Context(), refs[0])
	require.NoError(t, err)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)

	require.NoError(t, os.WriteFile(path, []byte("contexts: [invalid"), 0600))
	refs, err = p.Discover(t.Context())
	require.Error(t, err)
	require.Nil(t, refs)
}
