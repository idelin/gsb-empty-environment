package scrubber

import "sync"

type memoryEntry struct {
	version     uint64
	data        []byte
	healthy     bool
	quarantined bool
}

// MemoryBackend is an in-memory Backend. The version comparison and the
// state transition of the conditional marks happen under a single lock,
// so they are atomic with respect to Put and Delete.
type MemoryBackend struct {
	mu      sync.Mutex
	entries map[string]*memoryEntry
	nextVer uint64
}

// NewMemoryBackend creates an empty in-memory backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{entries: make(map[string]*memoryEntry)}
}

func (b *MemoryBackend) Get(key string) (Object, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok {
		return Object{}, ErrNotFound
	}
	if e.quarantined {
		return Object{}, ErrQuarantined
	}
	return Object{Key: key, Version: e.version, Data: append([]byte(nil), e.data...)}, nil
}

func (b *MemoryBackend) Put(key string, data []byte) (Object, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextVer++
	e := &memoryEntry{version: b.nextVer, data: append([]byte(nil), data...)}
	b.entries[key] = e
	return Object{Key: key, Version: e.version, Data: append([]byte(nil), e.data...)}, nil
}

func (b *MemoryBackend) Delete(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.entries[key]; !ok {
		return ErrNotFound
	}
	delete(b.entries, key)
	return nil
}

func (b *MemoryBackend) MarkHealthyIfVersion(key string, version uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok {
		return ErrNotFound
	}
	if e.version != version {
		return ErrStaleVersion
	}
	e.healthy = true
	return nil
}

func (b *MemoryBackend) QuarantineIfVersion(key string, version uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok {
		return ErrNotFound
	}
	if e.version != version {
		return ErrStaleVersion
	}
	e.quarantined = true
	return nil
}

func (b *MemoryBackend) Quarantined(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	return ok && e.quarantined
}
