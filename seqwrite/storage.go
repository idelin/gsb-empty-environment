package seqwrite

import (
	"io"
	"sync"
)

// MemStorage is an in-memory Storage, useful for tests and as a reference
// implementation. The exported fault-injection fields are read on every
// call under the lock, so tests can toggle them at any time.
type MemStorage struct {
	mu  sync.RWMutex
	buf []byte

	// Fault injection: when non-nil, the corresponding operation fails.
	WriteErr error
	SyncErr  error

	// Syncs counts successful Sync calls.
	Syncs int
}

// NewMemStorage returns an empty in-memory storage.
func NewMemStorage() *MemStorage { return &MemStorage{} }

// Len returns the current size of the storage.
func (m *MemStorage) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.buf)
}

func (m *MemStorage) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.WriteErr != nil {
		return 0, m.WriteErr
	}
	end := int(off) + len(p)
	if end > len(m.buf) {
		nbuf := make([]byte, end)
		copy(nbuf, m.buf)
		m.buf = nbuf
	}
	copy(m.buf[off:], p)
	return len(p), nil
}

func (m *MemStorage) ReadAt(p []byte, off int64) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if off >= int64(len(m.buf)) {
		return 0, io.EOF
	}
	n := copy(p, m.buf[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *MemStorage) Sync() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.SyncErr != nil {
		return m.SyncErr
	}
	m.Syncs++
	return nil
}
