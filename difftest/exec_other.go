//go:build !unix

package difftest

import (
	"os/exec"
	"time"
)

func setupCmd(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}
