package resource

import "sync"

// Store is an in-memory object store keyed by the canonical form of an ID.
type Store struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

// NewStore returns an empty in-memory object store.
func NewStore() *Store {
	return &Store{objects: make(map[string][]byte)}
}

// Put stores data under the ID's canonical key, replacing any previous
// object. The data is copied.
func (s *Store) Put(id ID, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[id.canonical] = append([]byte(nil), data...)
}

// Get returns the object stored under the ID's canonical key.
func (s *Store) Get(id ID) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.objects[id.canonical]
	return data, ok
}

// Cache is an in-memory download cache keyed by the same canonical form.
type Cache struct {
	mu    sync.Mutex
	items map[string][]byte
}

// NewCache returns an empty in-memory cache.
func NewCache() *Cache {
	return &Cache{items: make(map[string][]byte)}
}

// Get returns the cached bytes for id, if any.
func (c *Cache) Get(id ID) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, ok := c.items[id.canonical]
	return data, ok
}

// Put caches data under the ID's canonical key.
func (c *Cache) Put(id ID, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[id.canonical] = append([]byte(nil), data...)
}
