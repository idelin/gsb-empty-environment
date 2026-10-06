package keydir

import (
	"io"
	"os"
)

// memFile is an in-memory File. Only the first `flushed` bytes are
// durable; Crash drops the rest, modeling a power failure.
type memFile struct {
	data    []byte
	flushed int
}

func (f *memFile) Append(p []byte) (int, error) {
	f.data = append(f.data, p...)
	return len(p), nil
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memFile) Size() (int64, error) { return int64(len(f.data)), nil }

func (f *memFile) Truncate(size int64) error {
	f.data = f.data[:size]
	if f.flushed > int(size) {
		f.flushed = int(size)
	}
	return nil
}

func (f *memFile) Sync() error {
	f.flushed = len(f.data)
	return nil
}

func (f *memFile) Close() error { return nil }

// memFS is the injectable file model: tests use it to crash at chosen
// points, tear writes and corrupt individual files.
type memFS struct {
	files map[string]*memFile
}

func newMemFS() *memFS { return &memFS{files: make(map[string]*memFile)} }

func (m *memFS) Create(name string) (File, error) {
	f := &memFile{}
	m.files[name] = f
	return f, nil
}

func (m *memFS) Open(name string) (File, error) {
	f, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return f, nil
}

func (m *memFS) List() ([]string, error) {
	var names []string
	for name := range m.files {
		names = append(names, name)
	}
	return names, nil
}

func (m *memFS) Remove(name string) error {
	delete(m.files, name)
	return nil
}

func (m *memFS) Rename(src, dst string) error {
	f, ok := m.files[src]
	if !ok {
		return os.ErrNotExist
	}
	m.files[dst] = f
	delete(m.files, src)
	return nil
}

// Crash simulates power loss: every file keeps only its synced prefix.
func (m *memFS) Crash() {
	for _, f := range m.files {
		f.data = f.data[:f.flushed]
	}
}

// Corrupt flips the byte at off in name, modeling a damaged file.
func (m *memFS) Corrupt(name string, off int64) {
	f := m.files[name]
	f.data[off] ^= 0xFF
}

// appendRaw writes bytes directly to a file, bypassing the store.
func (m *memFS) appendRaw(name string, p []byte, sync bool) {
	f := m.files[name]
	f.data = append(f.data, p...)
	if sync {
		f.flushed = len(f.data)
	}
}
