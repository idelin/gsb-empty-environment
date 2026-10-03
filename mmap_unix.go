// +build darwin linux

package cbitmap

import (
	"os"
	"syscall"
)

// MmapFile memory-maps a file read-only. The returned slice can be passed
// directly to UnmarshalZeroCopy for zero-allocation parsing. Call Munmap
// when done.
func MmapFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size == 0 {
		return nil, &FormatError{"empty file"}
	}
	return syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
}

// Munmap releases a mapping obtained from MmapFile.
func Munmap(data []byte) error { return syscall.Munmap(data) }
