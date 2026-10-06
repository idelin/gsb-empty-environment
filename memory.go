package inspect

import (
	"context"
	"sync"
)

type storedObject struct {
	data    []byte
	version uint64
	status  Status
}

// MemoryBackend is an in-memory Backend. Every Put bumps the version and
// resets the status to StatusUnknown; SetStatusIfVersion compares and
// applies the status under the same lock, so the version check and the
// quarantine action are atomic.
type MemoryBackend struct {
	mu      sync.Mutex
	objects map[string]*storedObject
}

// NewMemoryBackend creates an empty in-memory backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{objects: make(map[string]*storedObject)}
}

// Put stores data under key, advancing the version and clearing any
// previously recorded status. It returns the new version.
func (m *MemoryBackend) Put(key string, data []byte) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		obj = &storedObject{}
		m.objects[key] = obj
	}
	obj.data = append([]byte(nil), data...)
	obj.version++
	obj.status = StatusUnknown
	return obj.version
}

// Delete removes key. It reports whether the object existed.
func (m *MemoryBackend) Delete(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objects[key]
	delete(m.objects, key)
	return ok
}

// Get implements Backend.
func (m *MemoryBackend) Get(_ context.Context, key string) (Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return Object{}, ErrNotFound
	}
	return Object{
		Key:     key,
		Data:    append([]byte(nil), obj.data...),
		Version: obj.version,
	}, nil
}

// SetStatusIfVersion implements Backend. The version comparison and the
// status update happen under a single lock acquisition.
func (m *MemoryBackend) SetStatusIfVersion(_ context.Context, key string, version uint64, status Status) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok || obj.version != version {
		return false, nil
	}
	obj.status = status
	return true, nil
}

// StatusOf returns the status recorded for key's current version.
func (m *MemoryBackend) StatusOf(key string) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return StatusUnknown, false
	}
	return obj.status, true
}

// Snapshot returns the data, version and status of key atomically.
func (m *MemoryBackend) Snapshot(key string) (data []byte, version uint64, status Status, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, exists := m.objects[key]
	if !exists {
		return nil, 0, StatusUnknown, false
	}
	return append([]byte(nil), obj.data...), obj.version, obj.status, true
}
