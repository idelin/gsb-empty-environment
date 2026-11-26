package plancache

import "sync"

// CacheScope distinguishes session-local entries (tied to one pooled
// connection's current principal) from GLOBAL entries (shared, but only ever
// reachable via a full per-principal key match).
type CacheScope int

const (
	// SESSION entries are destroyed on principal switch.
	SESSION CacheScope = iota
	// GLOBAL entries survive principal switches.
	GLOBAL
)

func (s CacheScope) String() string {
	if s == GLOBAL {
		return "GLOBAL"
	}
	return "SESSION"
}

type cacheEntry struct {
	key  CacheKey
	plan *Plan
}

// PlanCache is a keyed compiled-plan store. Lookup is exclusively by full
// CacheKey hash; there is deliberately no SQL-text or relation-name API.
type PlanCache struct {
	mu      sync.RWMutex
	scope   CacheScope
	entries map[string]cacheEntry
}

// NewPlanCache creates an empty cache of the given scope.
func NewPlanCache(scope CacheScope) *PlanCache {
	return &PlanCache{
		scope:   scope,
		entries: map[string]cacheEntry{},
	}
}

// Scope reports the cache scope.
func (c *PlanCache) Scope() CacheScope { return c.scope }

// Get returns the plan stored under the exact full key, or nil.
func (c *PlanCache) Get(key CacheKey) *Plan {
	hash := key.Hash()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.entries[hash]; ok {
		return e.plan
	}
	return nil
}

// Put stores a plan under its full key.
func (c *PlanCache) Put(plan *Plan) {
	hash := plan.Key.Hash()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[hash] = cacheEntry{key: plan.Key, plan: plan}
}

// Clear removes every entry (used on session principal switch).
func (c *PlanCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
}

// Len reports the number of cached plans.
func (c *PlanCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
