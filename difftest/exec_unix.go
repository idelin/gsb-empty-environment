//go:build unix

package difftest

import (
	"os/exec"
	"syscall"
	"time"
)

// setupCmd arranges for the whole process group to be killed on cancel, so
// grandchildren cannot keep stdout/stderr pipes open past the timeout.
func setupCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
}
