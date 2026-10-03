package paxos

import (
	"sync"

	"multipaxos/store"
)

// MemPersister is an in-memory Persister for tests.
type MemPersister struct {
	mu      sync.Mutex
	entries []store.Entry
}

// NewMemPersister creates an empty in-memory persister.
func NewMemPersister() *MemPersister { return &MemPersister{} }

// SaveAccepted appends e.
func (m *MemPersister) SaveAccepted(e store.Entry) error {
	m.mu.Lock()
	m.entries = append(m.entries, e)
	m.mu.Unlock()
	return nil
}

// Load returns all saved entries.
func (m *MemPersister) Load() ([]store.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.Entry(nil), m.entries...), nil
}

// Close is a no-op.
func (m *MemPersister) Close() error { return nil }

// FilePersister durably appends accepted entries to a store segment.
type FilePersister struct {
	mu     sync.Mutex
	w      *store.Writer
	path   string
	closed bool
}

// NewFilePersister creates a segment-backed persister at path, appending
// to any existing segment (crash-safe restart).
func NewFilePersister(path string) (*FilePersister, error) {
	w, err := store.OpenWriter(path, store.CurrentFormat)
	if err != nil {
		return nil, err
	}
	return &FilePersister{w: w, path: path}, nil
}

// SaveAccepted appends and fsyncs e.
func (p *FilePersister) SaveAccepted(e store.Entry) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if err := p.w.Append(e); err != nil {
		return err
	}
	return p.w.Sync()
}

// Load replays the segment via the zero-copy mmap reader, copying entries
// out before unmapping.
func (p *FilePersister) Load() ([]store.Entry, error) {
	seg, err := store.Open(p.path)
	if err != nil {
		return nil, err
	}
	defer seg.Close()
	var out []store.Entry
	err = seg.ForEach(func(e store.Entry) error {
		e.Command = append([]byte(nil), e.Command...) // copy out of the mapping
		out = append(out, e)
		return nil
	})
	return out, err
}

// Close flushes and closes the segment writer.
func (p *FilePersister) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.w.Close()
}
