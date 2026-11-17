//go:build darwin || linux

package execution

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// configureProcess isolates descendants so cancellation reaches the entire command.
// Final cleanup also removes descendants that outlive a successfully exited parent.
func configureProcess(cmd *exec.Cmd) func() error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}

		return err
	}
	cmd.WaitDelay = 2 * time.Second

	return func() error {
		if cmd.Process == nil {
			return nil
		}

		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}

		return err
	}
}
