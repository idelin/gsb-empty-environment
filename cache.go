package depseal

import "sync"

// cacheEntry is a verified blob stored for reuse. Entries are immutable once
// published: the stored content must never be modified in place.
type cacheEntry struct {
	sourceID string
	digest   Digest
	content  []byte
	attempts int
}

// cacheKey identifies a cached blob. It includes the tenant and the source
// identity alongside the digest: a same-named package fetched by an
// authorized tenant can therefore never be handed to an unauthorized tenant,
// even when the underlying content digest happens to match.
type cacheKey struct {
	tenant   string
	sourceID string
	digest   Digest
}

// contentCache is a concurrency-safe store of verified blobs.
type contentCache struct {
	mu      sync.RWMutex
	entries map[cacheKey]cacheEntry
}

func newContentCache() *contentCache {
	return &contentCache{entries: make(map[cacheKey]cacheEntry)}
}

// get returns a copy of the cached content so callers cannot corrupt the
// shared verified blob.
func (c *contentCache) get(key cacheKey) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	content := make([]byte, len(entry.content))
	copy(content, entry.content)
	return content, true
}

// put publishes a blob that has already been verified against key.digest.
func (c *contentCache) put(key cacheKey, entry cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		c.entries[key] = entry
	}
}

// len reports the number of cached entries and is used by tests.
func (c *contentCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
