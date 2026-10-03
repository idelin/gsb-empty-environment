//go:build !unix

package wal

import (
	"io"
	"os"
)

// Portable fallback: read the whole file. Still zero-copy with respect to the
// returned buffer (records alias it), just not demand-paged.
func mapFile(f *os.File, size int64) ([]byte, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

func unmapFile(b []byte) error { return nil }
