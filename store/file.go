package store

import (
	"io"
	"os"
	"sync"
)

// FileStorage adapts *os.File to Storage, tracking the logical size.
type FileStorage struct {
	f *os.File
}

// OpenFile opens (or creates) the log file at path.
func OpenFile(path string) (*FileStorage, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	return &FileStorage{f: f}, nil
}

func (s *FileStorage) WriteAt(p []byte, off int64) (int, error) { return s.f.WriteAt(p, off) }
func (s *FileStorage) ReadAt(p []byte, off int64) (int, error)  { return s.f.ReadAt(p, off) }
func (s *FileStorage) Truncate(size int64) error                { return s.f.Truncate(size) }
func (s *FileStorage) Sync() error                              { return s.f.Sync() }

// Size returns the current file size.
func (s *FileStorage) Size() int64 {
	fi, err := s.f.Stat()
	if err != nil {
		return 0
	}
	return fi.Size()
}

// Close flushes and closes the underlying file.
func (s *FileStorage) Close() error {
	if err := s.f.Sync(); err != nil {
		s.f.Close()
		return err
	}
	return s.f.Close()
}

// MemStorage is an in-memory Storage useful for tests and for the lossy
// network replication demo.
type MemStorage struct {
	mu  sync.Mutex
	buf []byte
}

// NewMemStorage creates an empty in-memory device.
func NewMemStorage() *MemStorage { return &MemStorage{} }

func (m *MemStorage) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	end := int(off) + len(p)
	if end > len(m.buf) {
		nb := make([]byte, end)
		copy(nb, m.buf)
		m.buf = nb
	}
	copy(m.buf[int(off):], p)
	return len(p), nil
}

func (m *MemStorage) ReadAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if int(off) >= len(m.buf) {
		return 0, io.EOF
	}
	n := copy(p, m.buf[int(off):])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *MemStorage) Truncate(size int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if int(size) < len(m.buf) {
		m.buf = m.buf[:size]
	}
	return nil
}

func (m *MemStorage) Sync() error { return nil }
func (m *MemStorage) Size() int64 { m.mu.Lock(); defer m.mu.Unlock(); return int64(len(m.buf)) }
