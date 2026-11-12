package netsec

import "sync"

// PinStore persists pins. Get returns the pin for host and port and whether
// one exists. Put replaces the pin for the same host and port.
//
// The pinner serializes its own read/modify/write sequence, but a store
// shared by several Pinner instances must provide its own concurrency
// guarantees.
type PinStore interface {
	Get(host string, port int) (Pin, bool, error)
	Put(pin Pin) error
}

// Store is an alias for PinStore.
type Store = PinStore

// MemoryStore is a concurrency-safe, process-local PinStore.
type MemoryStore struct {
	mu   sync.RWMutex
	pins map[Key]Pin
}

// NewMemoryStore returns an empty in-memory pin store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{pins: make(map[Key]Pin)}
}

// Get returns the pin for host and port.
func (m *MemoryStore) Get(host string, port int) (Pin, bool, error) {
	key := NewKey(host, port)
	m.mu.RLock()
	defer m.mu.RUnlock()
	pin, ok := m.pins[key]
	return pin, ok, nil
}

// Put stores or replaces pin.
func (m *MemoryStore) Put(pin Pin) error {
	key := pin.Key()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pins == nil {
		m.pins = make(map[Key]Pin)
	}
	m.pins[key] = pin
	return nil
}

// Load is an alias for Get.
func (m *MemoryStore) Load(host string, port int) (Pin, bool, error) {
	return m.Get(host, port)
}

// Save is an alias for Put.
func (m *MemoryStore) Save(pin Pin) error {
	return m.Put(pin)
}

// Delete removes the pin for host and port.
func (m *MemoryStore) Delete(host string, port int) {
	key := NewKey(host, port)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pins, key)
}

// Len returns the number of stored pins.
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pins)
}

// Snapshot returns a copy of all stored pins.
func (m *MemoryStore) Snapshot() map[Key]Pin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[Key]Pin, len(m.pins))
	for key, pin := range m.pins {
		out[key] = pin
	}
	return out
}
