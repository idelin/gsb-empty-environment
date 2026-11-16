package netsec

import (
	"crypto/sha256"
	"sync"
)

// cacheKey 为 (算法, 字典 ID, 证书链字节的 SHA-256)。
// 字典 ID 参与键值, 使不同字典/租户的条目天然隔离。
type cacheKey struct {
	alg    uint16
	dictID uint32
	sum    [sha256.Size]byte
}

type compressCache struct {
	mu     sync.Mutex
	m      map[cacheKey][]byte
	hits   uint64
	misses uint64
}

func newCompressCache() *compressCache {
	return &compressCache{m: make(map[cacheKey][]byte)}
}

func keyFor(alg uint16, dictID uint32, chain []byte) cacheKey {
	return cacheKey{alg: alg, dictID: dictID, sum: sha256.Sum256(chain)}
}

func (c *compressCache) get(k cacheKey) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	if ok {
		c.hits++
	} else {
		c.misses++
	}
	return v, ok
}

func (c *compressCache) put(k cacheKey, v []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
}

// stats 供测试与可观测性使用。
func (c *compressCache) stats() (entries, hits, misses uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return uint64(len(c.m)), c.hits, c.misses
}
