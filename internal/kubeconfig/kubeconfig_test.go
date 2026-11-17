package kubeconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/testutil"
	"k8s.io/client-go/tools/clientcmd"
)

func TestRelativeCredentialsAreEmbeddedWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	cfg, err := clientcmd.Load(testutil.Kubeconfig("before"))
	require.NoError(t, err)
	cfg.Clusters["cluster"].CertificateAuthority = "ca.pem"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ca.pem"), []byte("test-certificate"), 0600))
	data, err := clientcmd.Write(*cfg)
	require.NoError(t, err)
	path := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(path, data, 0600))
	loaded, err := File(path)
	require.NoError(t, err)
	selected, ns, err := Select(loaded, "test", "after")
	require.NoError(t, err)
	require.Equal(t, "after", ns)
	result, err := clientcmd.Load(selected)
	require.NoError(t, err)
	require.Equal(t, []byte("test-certificate"), result.Clusters["cluster"].CertificateAuthorityData)
	source, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, source)
}
