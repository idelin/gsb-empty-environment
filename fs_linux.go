//go:build linux

package netsec

import "syscall"

// Mkdirat creates a directory relative to dirFD.
func (OSFileSystem) Mkdirat(dirFD int, name string, mode uint32) error {
	return syscall.Mkdirat(dirFD, name, mode)
}

// Openat opens a path relative to dirFD.
func (OSFileSystem) Openat(dirFD int, name string, flags int, mode uint32) (int, error) {
	return syscall.Openat(dirFD, name, flags, mode)
}
