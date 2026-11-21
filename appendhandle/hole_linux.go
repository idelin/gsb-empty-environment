//go:build linux

package appendhandle

import (
	"errors"
	"syscall"
)

const (
	fallocFLPunchHole = 0x02
	fallocFLKeepSize  = 0x01
)

func punchHole(fd uintptr, off, length int64) error {
	return syscall.Fallocate(
		int(fd),
		fallocFLPunchHole|fallocFLKeepSize,
		off,
		length,
	)
}

func holeUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}
