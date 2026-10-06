package batch

import (
	"errors"
	"sync"
)

// ErrNotExist is returned by an FS when a path has no backing file.
var ErrNotExist = errors.New("batch: file does not exist")

// FS is the minimal read-only view of a source directory the runner needs.
// MemFS implements it for tests; production code can adapt os.DirFS etc.
type FS interface {
	ReadFile(path string) ([]byte, error)
}

// MemFS is an in-memory FS used as a virtual directory in tests. Tests may
// mutate it (Set/Delete) at any point to simulate source changes, including
// from inside a ProcessFunc to simulate mid-batch modifications.
type MemFS struct {
	mu    sync.RWMutex
	files map[string][]byte
}

func NewMemFS() *MemFS {
	return &MemFS{files: make(map[string][]byte)}
}

// Set creates or replaces a file in the virtual directory.
func (m *MemFS) Set(path string, content []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = append([]byte(nil), content...)
}

// Delete removes a file from the virtual directory.
func (m *MemFS) Delete(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, path)
}

func (m *MemFS) ReadFile(path string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	content, ok := m.files[path]
	if !ok {
		return nil, ErrNotExist
	}
	return append([]byte(nil), content...), nil
}
