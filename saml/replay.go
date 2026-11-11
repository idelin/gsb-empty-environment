package saml

import "sync"

// ReplayStore is the single-use ID boundary. Consume must atomically record
// an ID the first time it is seen and reject every later attempt.
type ReplayStore interface {
	Consume(id string) error
}

// MemoryReplay is a concurrency-safe one-time ID store. The first Consume for
// an ID wins; all concurrent or later callers receive ErrReplay.
type MemoryReplay struct {
	mu       sync.Mutex
	consumed map[string]struct{}
}

// NewMemoryReplay creates an empty replay store.
func NewMemoryReplay() *MemoryReplay {
	return &MemoryReplay{consumed: make(map[string]struct{})}
}

// Consume records id if unseen and returns nil; otherwise it returns
// ErrReplay without changing the store.
func (m *MemoryReplay) Consume(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, seen := m.consumed[id]; seen {
		return ErrReplay
	}
	m.consumed[id] = struct{}{}
	return nil
}

// Seen reports whether an ID has already been consumed. It is intended for
// diagnostics and tests.
func (m *MemoryReplay) Seen(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, seen := m.consumed[id]
	return seen
}
