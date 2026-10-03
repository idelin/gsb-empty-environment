//go:build unix

package wal

import (
	"os"
	"syscall"
)

func mapFile(f *os.File, size int64) ([]byte, error) {
	return syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
}

func unmapFile(b []byte) error {
	return syscall.Munmap(b)
}
