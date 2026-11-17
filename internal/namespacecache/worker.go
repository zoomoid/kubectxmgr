package namespacecache

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/zoomoid/kubectxmgr/internal/state"
)

// Start launches a detached, bounded refresh using the inherited KUBECONFIG.
// Only the context name is passed; credentials are not copied into job arguments.
func (c Cache) Start(executable, contextName string) error {
	path := filepath.Join(c.Disk.Root, "namespace-logs", state.Hash(c.Key)+".log")
	if err := state.PrivateDir(filepath.Dir(path)); err != nil {
		return err
	}

	log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()

	cmd := exec.Command(executable, "--state-directory", c.Disk.Root, "namespace", "--refresh-cache", contextName)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start namespace refresh: %w", err)
	}

	// The worker reports failures through status and stderr. Wait only reaps it
	// while the parent is alive; detached workers also survive the parent's exit.
	go func() { _ = cmd.Wait() }()

	return nil
}
