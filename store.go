package snapcatchup

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Store is the durable scratch space a follower uses while receiving a
// snapshot. Staged chunks and install-boundary markers must survive a crash
// so an interrupted install can be rolled forward or discarded on recovery.
type Store interface {
	Put(key string, data []byte) error
	Get(key string) ([]byte, error) // nil, nil when the key is absent
	Delete(key string) error
	DeletePrefix(prefix string) error
	List(prefix string) ([]string, error)
}

// MemStore is an in-memory Store. It outlives a Follower instance, which
// makes it handy for simulating crashes in tests.
type MemStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func NewMemStore() *MemStore { return &MemStore{data: make(map[string][]byte)} }

func (m *MemStore) Put(key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.data[key] = cp
	return nil
}

func (m *MemStore) Get(key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return nil, nil
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp, nil
}

func (m *MemStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *MemStore) DeletePrefix(prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			delete(m.data, k)
		}
	}
	return nil
}

func (m *MemStore) List(prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// FileStore persists keys as files under a directory.
type FileStore struct{ dir string }

func NewFileStore(dir string) *FileStore { return &FileStore{dir: dir} }

func (f *FileStore) path(key string) string {
	return filepath.Join(f.dir, filepath.FromSlash(key))
}

func (f *FileStore) Put(key string, data []byte) error {
	p := f.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (f *FileStore) Get(key string) ([]byte, error) {
	b, err := os.ReadFile(f.path(key))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return b, err
}

func (f *FileStore) Delete(key string) error {
	err := os.Remove(f.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (f *FileStore) DeletePrefix(prefix string) error {
	keys, err := f.List(prefix)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := f.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

func (f *FileStore) List(prefix string) ([]string, error) {
	if _, err := os.Stat(f.dir); os.IsNotExist(err) {
		return nil, nil
	}
	var out []string
	err := filepath.Walk(f.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(f.dir, p)
		if err != nil {
			return err
		}
		if key := filepath.ToSlash(rel); strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
