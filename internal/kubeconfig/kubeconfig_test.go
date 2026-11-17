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

func TestNamespaceChangesOnlyItsOriginInMergedConfig(t *testing.T) {
	dir := t.TempDir()
	clusterPath, contextPath := filepath.Join(dir, "cluster"), filepath.Join(dir, "contexts")
	cfg, err := clientcmd.Load(testutil.Kubeconfig("before"))
	require.NoError(t, err)
	contexts := cfg.DeepCopy()
	contexts.Clusters = nil
	contexts.AuthInfos = nil
	contexts.CurrentContext = ""
	contexts.Contexts["other"] = contexts.Contexts["test"].DeepCopy()
	cfg.Contexts = nil
	data, err := clientcmd.Write(*cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(clusterPath, data, 0600))
	contextData, err := clientcmd.Write(*contexts)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(contextPath, contextData, 0600))
	t.Setenv("KUBECONFIG", clusterPath+string(os.PathListSeparator)+contextPath)

	require.NoError(t, SetNamespace("test", "development"))
	unchanged, err := os.ReadFile(clusterPath)
	require.NoError(t, err)
	require.Equal(t, data, unchanged)
	changed, err := clientcmd.LoadFromFile(contextPath)
	require.NoError(t, err)
	require.Equal(t, "development", changed.Contexts["test"].Namespace)
	require.Equal(t, "before", changed.Contexts["other"].Namespace)
	require.ErrorContains(t, SetNamespace("another-context", "incorrect"), "active context changed")
}
