//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package netsec

import (
	"fmt"
	"os"
	"syscall"
)

func fileOwnerUID(path string) (uint32, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("netsec: ownership metadata unavailable for %s", path)
	}
	return uint32(stat.Uid), nil
}
