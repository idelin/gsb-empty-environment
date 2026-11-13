package netsec

import "sync"

// GlobalCache holds compiled plans across sessions and principals. Entries are
// keyed by the complete PlanKey, which includes the principal and every policy
// dependency. A new principal can therefore use the global cache only through
// its own key.
type GlobalCache struct {
	mu      sync.RWMutex
	entries map[PlanKey]*Plan
	hits    uint64
	misses  uint64
}

// PlanCache is an alias for GlobalCache.
type PlanCache = GlobalCache

// NewGlobalCache creates an empty cache.
func NewGlobalCache() *GlobalCache {
	return &GlobalCache{entries: make(map[PlanKey]*Plan)}
}

// NewPlanCache is an alias for NewGlobalCache.
func NewPlanCache() *GlobalCache { return NewGlobalCache() }

// Get returns a plan by its complete key. Callers must still run
// SecuritySnapshot.Revalidate against a pinned snapshot before execution.
func (c *GlobalCache) Get(key PlanKey) (*Plan, bool) {
	c.mu.RLock()
	plan, ok := c.entries[key]
	c.mu.RUnlock()
	if ok {
		c.mu.Lock()
		c.hits++
		c.mu.Unlock()
		return plan, true
	}
	c.mu.Lock()
	c.misses++
	c.mu.Unlock()
	return nil, false
}

// Lookup is an alias for Get.
func (c *GlobalCache) Lookup(key PlanKey) (*Plan, bool) { return c.Get(key) }

// Put stores an immutable plan. Existing entries are replaced only by an
// identical key; they are never mutated in place.
func (c *GlobalCache) Put(plan *Plan) {
	if plan == nil {
		return
	}
	c.mu.Lock()
	c.entries[plan.Key()] = plan
	c.mu.Unlock()
}

// Store is an alias for Put.
func (c *GlobalCache) Store(plan *Plan) { c.Put(plan) }

// Delete removes one entry. It is used when pre-execution review discovers
// that a same-key entry is stale anyway.
func (c *GlobalCache) Delete(key PlanKey) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// Len returns the number of cached plans.
func (c *GlobalCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Count is an alias for Len.
func (c *GlobalCache) Count() int { return c.Len() }

// Reset removes every entry. Statistics are preserved.
func (c *GlobalCache) Reset() {
	c.mu.Lock()
	c.entries = make(map[PlanKey]*Plan)
	c.mu.Unlock()
}

// Stats returns cumulative hit and miss counters.
func (c *GlobalCache) Stats() (hits, misses uint64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses
}
