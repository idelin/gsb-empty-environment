// +build !darwin,!linux

package cbitmap

import "errors"

// MmapFile is unsupported on this platform.
func MmapFile(path string) ([]byte, error) {
	return nil, errors.New("cbitmap: mmap unsupported on this platform")
}

// Munmap is unsupported on this platform.
func Munmap(data []byte) error { return nil }
