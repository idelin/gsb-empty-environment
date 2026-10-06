package recovery

import "sync"

// MemFS is an in-memory file system used as the artifact store. Tasks read
// their inputs and write their outputs through it, and tests use it to
// corrupt or edit files directly.
type MemFS struct {
	mu    sync.Mutex
	files map[string][]byte
}

func NewMemFS() *MemFS {
	return &MemFS{files: make(map[string][]byte)}
}

// Write stores data at path, replacing any previous content.
func (m *MemFS) Write(path string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.files[path] = cp
}

// Read returns the content at path and whether it exists.
func (m *MemFS) Read(path string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[path]
	if !ok {
		return nil, false
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, true
}

// Delete removes path from the store.
func (m *MemFS) Delete(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, path)
}
