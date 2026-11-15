package netsec

import (
	"container/list"
	"crypto/sha256"
	"sync"
)

// CacheKey is the exact cache identity required for certificate compression:
// (algorithm, dictionary ID, SHA-256 of the certificate-chain bytes).
//
// Dictionary IDs are globally unique per tenant in DictionaryRegistry, so this
// tuple cannot alias a dictionary owned by another tenant.
type CacheKey struct {
	Algorithm    Algorithm
	DictionaryID DictionaryID
	ChainSHA256  [sha256.Size]byte
}

// NewCacheKey computes the cache key for chainBytes.
func NewCacheKey(algorithm Algorithm, dictionaryID DictionaryID, chainBytes []byte) CacheKey {
	return CacheKey{
		Algorithm:    algorithm,
		DictionaryID: dictionaryID,
		ChainSHA256:  sha256.Sum256(chainBytes),
	}
}

type cacheEntry struct {
	key        CacheKey
	compressed []byte
}

// CompressionCache is a bounded LRU cache of compressed certificate messages.
// The zero value is usable and uses DefaultCacheEntries.
type CompressionCache struct {
	mutex       sync.Mutex
	maxEntries  int
	entries     map[CacheKey]*list.Element
	recencyList *list.List
}

// DefaultCacheEntries is the entry limit used by a zero-value cache.
const DefaultCacheEntries = 1024

// NewCompressionCache creates a cache bounded to maxEntries entries. A
// non-positive value selects DefaultCacheEntries.
func NewCompressionCache(maxEntries int) *CompressionCache {
	if maxEntries <= 0 {
		maxEntries = DefaultCacheEntries
	}
	return &CompressionCache{
		maxEntries:  maxEntries,
		entries:     make(map[CacheKey]*list.Element),
		recencyList: list.New(),
	}
}

// Get returns a copy of the cached compressed bytes.
func (cache *CompressionCache) Get(key CacheKey) ([]byte, bool) {
	if cache == nil {
		return nil, false
	}
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	element, ok := cache.entries[key]
	if !ok {
		return nil, false
	}
	cache.recencyList.MoveToFront(element)
	entry := element.Value.(*cacheEntry)
	return append([]byte(nil), entry.compressed...), true
}

// Put stores compressed bytes, evicting the least recently used entry when the
// cache is full.
func (cache *CompressionCache) Put(key CacheKey, compressed []byte) {
	if cache == nil {
		return
	}
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	cache.putLocked(key, compressed)
}

func (cache *CompressionCache) putLocked(key CacheKey, compressed []byte) {
	if cache.maxEntries <= 0 {
		cache.maxEntries = DefaultCacheEntries
	}
	if cache.entries == nil {
		cache.entries = make(map[CacheKey]*list.Element)
	}
	if cache.recencyList == nil {
		cache.recencyList = list.New()
	}

	value := append([]byte(nil), compressed...)
	if element, ok := cache.entries[key]; ok {
		entry := element.Value.(*cacheEntry)
		entry.compressed = value
		cache.recencyList.MoveToFront(element)
		return
	}

	element := cache.recencyList.PushFront(&cacheEntry{key: key, compressed: value})
	cache.entries[key] = element
	for len(cache.entries) > cache.maxEntries {
		oldest := cache.recencyList.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(*cacheEntry)
		delete(cache.entries, entry.key)
		cache.recencyList.Remove(oldest)
	}
}

// GetOrCompute returns a cached value or computes and stores it. The boolean
// result reports whether the value came from the cache.
func (cache *CompressionCache) GetOrCompute(key CacheKey, compute func() ([]byte, error)) ([]byte, bool, error) {
	if compressed, ok := cache.Get(key); ok {
		return compressed, true, nil
	}
	compressed, err := compute()
	if err != nil {
		return nil, false, err
	}
	cache.Put(key, compressed)
	return append([]byte(nil), compressed...), false, nil
}

// Len returns the number of cached entries.
func (cache *CompressionCache) Len() int {
	if cache == nil {
		return 0
	}
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	return len(cache.entries)
}

// Clear removes all entries.
func (cache *CompressionCache) Clear() {
	if cache == nil {
		return
	}
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	cache.entries = make(map[CacheKey]*list.Element)
	cache.recencyList = list.New()
}
