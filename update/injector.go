package update

import (
	"context"
	"sync"
)

// ConflictInjector wraps a Store and forces the first Failures CompareAndSwap
// calls to fail with ErrConflict, simulating concurrent writers. It is used
// to verify that retried updates do not leak side effects from failed
// attempts.
type ConflictInjector struct {
	Inner Store

	mu       sync.Mutex
	Failures int
	// Attempts counts CompareAndSwap calls that reached the injector.
	Attempts int
}

func (c *ConflictInjector) Read(ctx context.Context, key string) ([]byte, uint64, error) {
	return c.Inner.Read(ctx, key)
}

func (c *ConflictInjector) CompareAndSwap(ctx context.Context, key string, value []byte, expectedVersion uint64) (uint64, error) {
	c.mu.Lock()
	c.Attempts++
	fail := c.Failures > 0
	if fail {
		c.Failures--
	}
	c.mu.Unlock()
	if fail {
		return 0, ErrConflict
	}
	return c.Inner.CompareAndSwap(ctx, key, value, expectedVersion)
}
