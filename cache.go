package netsec

import (
	"container/list"
	"sync"
)

const (
	defaultCacheEntries = 256
	defaultCacheBytes   = 16 << 20
)

type cacheScope struct {
	tenant string
	key    CacheKey
}

type cacheEntry struct {
	scope cacheScope
	data  []byte
}

// CertificateCache stores already-compressed certificate message payloads.
// Entries are scoped by tenant as well as by CacheKey, so two tenants that
// happen to use the same dictionary ID cannot observe each other's cached
// compressed chain. The cache itself is safe for concurrent use.
type CertificateCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	bytes      int
	entries    map[cacheScope]*list.Element
	lru        *list.List
}

// NewCertificateCache creates a bounded cache. Limits can be omitted or
// supplied as maxEntries and maxBytes. Non-positive limits select the package
// defaults.
func NewCertificateCache(limits ...int) *CertificateCache {
	maxEntries := defaultCacheEntries
	maxBytes := defaultCacheBytes
	if len(limits) > 0 {
		maxEntries = limits[0]
	}
	if len(limits) > 1 {
		maxBytes = limits[1]
	}
	if maxEntries <= 0 {
		maxEntries = defaultCacheEntries
	}
	if maxBytes <= 0 {
		maxBytes = defaultCacheBytes
	}
	return &CertificateCache{
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
		entries:    make(map[cacheScope]*list.Element),
		lru:        list.New(),
	}
}

// DefaultCertificateCache returns a cache with bounded defaults.
func DefaultCertificateCache() *CertificateCache {
	return NewCertificateCache(defaultCacheEntries, defaultCacheBytes)
}

// Get retrieves compressed certificate bytes for a tenant. The returned slice
// is a copy and cannot be used to mutate the cached value.
func (c *CertificateCache) Get(tenant string, key CacheKey) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	tenant = CanonicalSNI(tenant)
	scope := cacheScope{tenant: tenant, key: key}

	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[scope]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(element)
	entry := element.Value.(*cacheEntry)
	return append([]byte(nil), entry.data...), true
}

// Put stores compressed certificate bytes for a tenant. Oversized entries are
// simply not cached.
func (c *CertificateCache) Put(tenant string, key CacheKey, compressed []byte) {
	if c == nil || len(compressed) == 0 || len(compressed) > c.maxBytes {
		return
	}
	tenant = CanonicalSNI(tenant)
	scope := cacheScope{tenant: tenant, key: key}
	stored := append([]byte(nil), compressed...)

	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[scope]; ok {
		entry := element.Value.(*cacheEntry)
		c.bytes += len(stored) - len(entry.data)
		entry.data = stored
		c.lru.MoveToFront(element)
		c.evictLocked(scope)
		return
	}

	element := c.lru.PushFront(&cacheEntry{scope: scope, data: stored})
	c.entries[scope] = element
	c.bytes += len(stored)
	c.evictLocked(scope)
}

// DeleteTenant drops all cache entries for a tenant, for example when its
// dictionary configuration is retired.
func (c *CertificateCache) DeleteTenant(tenant string) {
	if c == nil {
		return
	}
	tenant = CanonicalSNI(tenant)
	c.mu.Lock()
	defer c.mu.Unlock()
	for scope, element := range c.entries {
		if scope.tenant != tenant {
			continue
		}
		entry := element.Value.(*cacheEntry)
		c.bytes -= len(entry.data)
		c.lru.Remove(element)
		delete(c.entries, scope)
	}
}

// Len reports the number of cached entries.
func (c *CertificateCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *CertificateCache) evictLocked(keep cacheScope) {
	for len(c.entries) > c.maxEntries || c.bytes > c.maxBytes {
		element := c.lru.Back()
		if element == nil {
			return
		}
		entry := element.Value.(*cacheEntry)
		if entry.scope == keep && len(c.entries) == 1 {
			return
		}
		c.bytes -= len(entry.data)
		c.lru.Remove(element)
		delete(c.entries, entry.scope)
	}
}
