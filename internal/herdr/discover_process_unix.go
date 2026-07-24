//go:build unix

package herdr

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// prepareCommand makes cancellation terminate the whole subprocess tree. This
// also prevents descendants that inherited stdout/stderr from keeping Cmd.Wait
// blocked after the direct child exits.
func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 500 * time.Millisecond
}
