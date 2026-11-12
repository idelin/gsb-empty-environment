package netsec

import "sync"

// MapAttributeStore is an in-memory AttributeReader. It is useful for tests and
// for engines that manage attributes through a non-OS backend.
type MapAttributeStore struct {
	mu    sync.RWMutex
	attrs map[string]map[string][]byte
}

// NewMapAttributeStore returns an empty store.
func NewMapAttributeStore() *MapAttributeStore {
	return &MapAttributeStore{attrs: make(map[string]map[string][]byte)}
}

// Set stores a copy of data for path and name.
func (s *MapAttributeStore) Set(path, name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attrs == nil {
		s.attrs = make(map[string]map[string][]byte)
	}
	if s.attrs[path] == nil {
		s.attrs[path] = make(map[string][]byte)
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	s.attrs[path][name] = cp
}

// Delete removes an attribute.
func (s *MapAttributeStore) Delete(path, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if perPath, ok := s.attrs[path]; ok {
		delete(perPath, name)
		if len(perPath) == 0 {
			delete(s.attrs, path)
		}
	}
}

// ReadAttribute implements AttributeReader.
func (s *MapAttributeStore) ReadAttribute(path, name string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.attrs[path][name]
	if !ok {
		return nil, ErrAttributeMissing
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

// SetAttribute writes an attribute through the OS.
func SetAttribute(path, name string, data []byte) error {
	return platformSetXattr(path, name, data)
}

// RemoveAttribute removes an attribute through the OS.
func RemoveAttribute(path, name string) error {
	return platformRemoveXattr(path, name)
}
