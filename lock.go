package netsec

import "sync"

type keyedMutex struct {
	mu      sync.Mutex
	entries map[string]*keyedMutexEntry
}

type keyedMutexEntry struct {
	mu   sync.Mutex
	refs int
}

// lock serializes work for one key and removes idle entries. The returned
// function must be called exactly once.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.entries == nil {
		k.entries = make(map[string]*keyedMutexEntry)
	}
	entry := k.entries[key]
	if entry == nil {
		entry = &keyedMutexEntry{}
		k.entries[key] = entry
	}
	entry.refs++
	k.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		k.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(k.entries, key)
		}
		k.mu.Unlock()
	}
}
