//go:build linux

package netsec

import (
	"errors"
	"syscall"
)

func platformGetXAttr(path, name string, dest []byte) (int, error) {
	return syscall.Getxattr(path, name, dest)
}

func platformSetXAttr(path, name string, data []byte, flags int) error {
	return syscall.Setxattr(path, name, data, flags)
}

func platformRemoveXAttr(path, name string) error {
	return syscall.Removexattr(path, name)
}

func isNoAttr(err error) bool {
	return errors.Is(err, syscall.ENODATA)
}
