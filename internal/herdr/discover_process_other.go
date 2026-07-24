//go:build !unix

package herdr

import (
	"os/exec"
	"time"
)

// prepareCommand bounds pipe cleanup on platforms without Unix process groups.
func prepareCommand(cmd *exec.Cmd) {
	cmd.WaitDelay = 500 * time.Millisecond
}
