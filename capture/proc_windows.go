//go:build windows

package capture

import "os/exec"

// Windows has no POSIX process groups; the direct child is terminated.
func setNewProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
