package netsec

import (
	"context"
	"sync"
)

// MemoryReplayCache is an in-process ReplayCache backed by a digest set.
// It is safe for concurrent use. Hosts that need durable or cross-instance
// protection should implement ReplayCache against their own store.
type MemoryReplayCache struct {
	mu   sync.Mutex
	seen map[Digest]struct{}
}

// NewMemoryReplayCache creates an empty replay cache.
func NewMemoryReplayCache() *MemoryReplayCache {
	return &MemoryReplayCache{seen: make(map[Digest]struct{})}
}

// Seen reports whether digest was marked on an earlier successful completion.
func (c *MemoryReplayCache) Seen(_ context.Context, digest Digest) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.seen[digest]
	return ok, nil
}

// Mark records digest as consumed.
func (c *MemoryReplayCache) Mark(_ context.Context, digest Digest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen[digest] = struct{}{}
	return nil
}
