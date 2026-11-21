//go:build darwin

package appendhandle

import (
	"errors"
	"syscall"
	"unsafe"
)

const fPunchHole = 99

type fpunchhole struct {
	flags    uint32
	reserved uint32
	offset   int64
	length   int64
}

func punchHole(fd uintptr, off, length int64) error {
	arg := fpunchhole{offset: off, length: length}
	_, _, errno := syscall.Syscall(
		syscall.SYS_FCNTL,
		fd,
		uintptr(fPunchHole),
		uintptr(unsafe.Pointer(&arg)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func holeUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}
