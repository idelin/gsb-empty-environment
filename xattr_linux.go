//go:build linux

package netsec

import (
	"errors"
	"syscall"
)

func platformReadXattr(path, name string) ([]byte, error) {
	size, err := syscall.Getxattr(path, name, nil)
	if err != nil {
		if errors.Is(err, syscall.ENODATA) {
			return nil, ErrAttributeMissing
		}
		return nil, err
	}
	if size == 0 {
		return []byte{}, nil
	}

	buf := make([]byte, size)
	n, err := syscall.Getxattr(path, name, buf)
	if err != nil {
		if errors.Is(err, syscall.ENODATA) {
			return nil, ErrAttributeMissing
		}
		return nil, err
	}
	return buf[:n], nil
}

func platformSetXattr(path, name string, data []byte) error {
	if err := syscall.Setxattr(path, name, data, 0); err != nil {
		if errors.Is(err, syscall.ENODATA) {
			return ErrAttributeMissing
		}
		return err
	}
	return nil
}

func platformRemoveXattr(path, name string) error {
	if err := syscall.Removexattr(path, name); err != nil {
		if errors.Is(err, syscall.ENODATA) {
			return ErrAttributeMissing
		}
		return err
	}
	return nil
}
