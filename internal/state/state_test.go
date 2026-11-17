package state

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAtomicReadersAndPermissions(t *testing.T) {
	path := t.TempDir() + "/private/state.yaml"
	require.NoError(t, Atomic(path, Status{Version: Version, State: "old"}))
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			for range 10 {
				s, err := Read[Status](path)
				if err != nil {
					errs <- err
					return
				}
				if s.Version != Version || (s.State != "old" && s.State != "new") {
					errs <- errors.New("torn state")
					return
				}
			}
		})
	}
	for range 10 {
		require.NoError(t, Atomic(path, Status{Version: Version, State: "new"}))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestLockAcrossProcessesAndCancellation(t *testing.T) {
	d := Disk{Root: t.TempDir()}
	unlock, err := d.Lock(t.Context(), "store", true)
	require.NoError(t, err)
	defer unlock()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockChild$")
	cmd.Env = append(os.Environ(), "KUBECTXMGR_LOCK_CHILD="+d.Root)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", b)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = d.Lock(ctx, "store", true)
	require.ErrorIs(t, err, context.Canceled)
	_, err = d.Lock(t.Context(), "store", false)
	require.ErrorIs(t, err, ErrBusy)
}

func TestLockChild(t *testing.T) {
	root := os.Getenv("KUBECTXMGR_LOCK_CHILD")
	if root == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := (Disk{Root: root}).Lock(ctx, "store", false)
	require.ErrorIs(t, err, ErrBusy)
}

func TestLastSelectionConcurrentWriters(t *testing.T) {
	d := Disk{Root: t.TempDir()}
	var wg sync.WaitGroup
	for _, value := range []string{"a", "b"} {
		wg.Go(func() {
			require.NoError(t, d.SaveSelection(t.Context(), Selection{Context: value, Namespace: value, Kubeconfig: []byte(value)}))
		})
	}
	wg.Wait()
	last, err := d.Last()
	require.NoError(t, err)
	require.Equal(t, last.Context, last.Namespace)
	require.Equal(t, last.Context, string(last.Kubeconfig))
}
