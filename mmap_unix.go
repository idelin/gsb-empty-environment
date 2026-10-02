// +build darwin linux

package roaring

import (
	"os"
	"syscall"
)

// MappedBitmap is a Bitmap decoded in zero-copy mode directly from a
// memory-mapped file. Call Close to release the mapping.
type MappedBitmap struct {
	*Bitmap
	data []byte
}

// OpenMapped memory-maps the file at path read-only and decodes the bitmap
// it contains without copying container payloads. The bitmap stays valid
// until Close is called.
func OpenMapped(path string) (*MappedBitmap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := fi.Size()
	if size == 0 {
		f.Close()
		return nil, ErrInvalidFormat
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	f.Close()
	if err != nil {
		return nil, err
	}
	bm, err := Deserialize(data)
	if err != nil {
		syscall.Munmap(data)
		return nil, err
	}
	return &MappedBitmap{Bitmap: bm, data: data}, nil
}

// Close unmaps the underlying file. The bitmap must not be used afterwards.
func (m *MappedBitmap) Close() error {
	m.Bitmap = nil
	return syscall.Munmap(m.data)
}
