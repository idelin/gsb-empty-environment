//go:build darwin

package netsec

import (
	"runtime"
	"syscall"
	"unsafe"
)

const (
	sysOpenat  = 463
	sysMkdirat = 475
)

// Mkdirat creates a directory relative to dirFD.
func (OSFileSystem) Mkdirat(dirFD int, name string, mode uint32) error {
	path, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(
		sysMkdirat,
		uintptr(dirFD),
		uintptr(unsafe.Pointer(path)),
		uintptr(mode),
		0, 0, 0,
	)
	runtime.KeepAlive(path)
	if errno != 0 {
		return errno
	}
	return nil
}

// Openat opens a path relative to dirFD.
func (OSFileSystem) Openat(dirFD int, name string, flags int, mode uint32) (int, error) {
	path, err := syscall.BytePtrFromString(name)
	if err != nil {
		return -1, err
	}
	fd, _, errno := syscall.Syscall6(
		sysOpenat,
		uintptr(dirFD),
		uintptr(unsafe.Pointer(path)),
		uintptr(flags),
		uintptr(mode),
		0, 0,
	)
	runtime.KeepAlive(path)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}
