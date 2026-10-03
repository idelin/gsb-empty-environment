//go:build unix

package store

import (
	"os"
	"syscall"
)

func mmapReadOnly(f *os.File, size int64) ([]byte, func([]byte) error, error) {
	if size == 0 {
		return nil, nil, nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, err
	}
	return data, syscall.Munmap, nil
}
