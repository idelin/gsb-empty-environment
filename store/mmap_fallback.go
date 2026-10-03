//go:build !unix

package store

import (
	"io"
	"os"
)

func mmapReadOnly(f *os.File, size int64) ([]byte, func([]byte) error, error) {
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	return data, func([]byte) error { return nil }, nil
}
