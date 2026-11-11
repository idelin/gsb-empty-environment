//go:build darwin

package rootedpublish

import "syscall"

func pid() int {
	// Raw syscall number for getpid on macOS is 20 on every architecture.
	r1, _, _ := syscall.Syscall(20, 0, 0, 0)
	return int(r1)
}
