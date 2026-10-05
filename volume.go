package migsim

import (
	"sort"
	"sync"
)

// Volume is a physical storage target holding block replicas keyed by string.
// A Volume is safe for concurrent use.
type Volume struct {
	id   string
	mu   sync.Mutex
	data map[string][]byte
}

// NewVolume creates an empty volume with the given ID.
func NewVolume(id string) *Volume {
	return &Volume{id: id, data: make(map[string][]byte)}
}

// ID returns the volume identifier.
func (v *Volume) ID() string { return v.id }

// Put stores a copy of b under key, replacing any previous content.
func (v *Volume) Put(key string, b []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.data[key] = append([]byte(nil), b...)
}

// Get returns a copy of the content stored under key.
func (v *Volume) Get(key string) ([]byte, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	b, ok := v.data[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

// Delete removes key, reporting whether it existed.
func (v *Volume) Delete(key string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.data[key]; !ok {
		return false
	}
	delete(v.data, key)
	return true
}

// Has reports whether key exists.
func (v *Volume) Has(key string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.data[key]
	return ok
}

// Keys returns the sorted list of keys present in the volume.
func (v *Volume) Keys() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	keys := make([]string, 0, len(v.data))
	for k := range v.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
