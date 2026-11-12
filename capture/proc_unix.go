//go:build !windows

package capture

import (
	"os/exec"
	"syscall"
)

// setNewProcessGroup makes the child a group leader so a single negative-pid
// signal reaches the whole trial tree instead of only the direct child.
func setNewProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup terminates every process in the trial's process group. A
// second SIGKILL guards against trials ignoring SIGTERM.
func killProcessGroup(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
