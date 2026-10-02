package occ

import "sync"

// LockStore is a pessimistic, single-mutex key/value store used as the
// baseline to compare OCC against. Every operation (read or write)
// goes through one global RWMutex, and a read-modify-write "transaction"
// holds the write lock for its whole duration.
type LockStore struct {
	mu   sync.RWMutex
	data map[string]string
}

// NewLockStore creates an empty LockStore.
func NewLockStore() *LockStore {
	return &LockStore{data: make(map[string]string)}
}

// Get reads a key under the global read lock.
func (l *LockStore) Get(key string) string {
	l.mu.RLock()
	v := l.data[key]
	l.mu.RUnlock()
	return v
}

// Put writes a key under the global write lock.
func (l *LockStore) Put(key, value string) {
	l.mu.Lock()
	l.data[key] = value
	l.mu.Unlock()
}

// Transact runs fn while holding the global write lock, so the whole
// read-modify-write is serialized against every other transaction.
func (l *LockStore) Transact(fn func(get func(string) string, set func(string, string))) {
	l.mu.Lock()
	get := func(k string) string { return l.data[k] }
	set := func(k, v string) { l.data[k] = v }
	fn(get, set)
	l.mu.Unlock()
}
