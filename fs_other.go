//go:build !darwin && !linux

package netsec

import "errors"

var errUnsupported = errors.New("netsec: descriptor-relative directory traversal is unsupported on this platform")

// Mkdirat is not implemented on this platform.
func (OSFileSystem) Mkdirat(dirFD int, name string, mode uint32) error {
	return errUnsupported
}

// Openat is not implemented on this platform.
func (OSFileSystem) Openat(dirFD int, name string, flags int, mode uint32) (int, error) {
	return -1, errUnsupported
}
