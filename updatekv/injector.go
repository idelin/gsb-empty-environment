package updatekv

import (
	"context"
	"sync"
)

// ConflictInjector wraps a Store and fails the first N CompareAndSwap
// calls with ErrConflict, simulating contention. It is used to verify
// that updates retry and that side effects of losing attempts are never
// delivered.
type ConflictInjector struct {
	inner Store

	mu        sync.Mutex
	remaining int
	injected  int
}

func NewConflictInjector(inner Store, failures int) *ConflictInjector {
	return &ConflictInjector{inner: inner, remaining: failures}
}

// Injected reports how many conflicts have been injected so far.
func (c *ConflictInjector) Injected() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.injected
}

func (c *ConflictInjector) Read(ctx context.Context, key string) (Record, error) {
	return c.inner.Read(ctx, key)
}

func (c *ConflictInjector) CompareAndSwap(ctx context.Context, key string, value []byte, expectedVersion uint64) (uint64, error) {
	c.mu.Lock()
	if c.remaining > 0 {
		c.remaining--
		c.injected++
		c.mu.Unlock()
		return 0, ErrConflict
	}
	c.mu.Unlock()
	return c.inner.CompareAndSwap(ctx, key, value, expectedVersion)
}
