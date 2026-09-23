package proof

import (
	"crypto/sha256"
	"fmt"
	"sync"
)

// ChunkCache caches verified chunk bytes. Every entry is bound to the package
// id AND package version AND chunk index AND committed chunk hash, so a chunk
// cached for one package/version can never satisfy another:
//
//   - old proof spliced onto a new package -> different pkg id/version -> miss
//   - two versions of the same package      -> version differs         -> miss
//   - corrupt/modified cached bytes         -> hash re-check fails     -> rejected
//
// A cache hit never bypasses the hash: bytes pulled from cache are re-hashed
// against the manifest entry before use. Old cache data is therefore useless
// without a successful hash re-verification.
type ChunkCache struct {
	mu      sync.Mutex
	entries map[string][]byte
}

func NewChunkCache() *ChunkCache { return &ChunkCache{entries: make(map[string][]byte)} }

func cacheKey(pkgID string, version uint64, index uint32, h [32]byte) string {
	idHash := sha256.Sum256([]byte(pkgID))
	return fmt.Sprintf("%x:%d:%d:%x", idHash[:8], version, index, h[:8])
}

// Put stores bytes previously verified for (pkgID, version, index, hash). The
// caller is expected to have passed them through a Verifier; Put still rechecks
// the hash so poisoned cache writes cannot install wrong bytes.
func (c *ChunkCache) Put(pkgID string, version uint64, index uint32, want [32]byte, p []byte) error {
	if ChunkHash(index, p) != want {
		return fmt.Errorf("%w: cache put chunk %d", ErrChunkHash, index)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	c.entries[cacheKey(pkgID, version, index, want)] = cp
	return nil
}

// Get returns cached bytes only when the exact (pkgID, version, index, hash)
// binding exists AND the bytes hash correctly right now.
func (c *ChunkCache) Get(pkgID string, version uint64, index uint32, want [32]byte) ([]byte, bool) {
	c.mu.Lock()
	p, ok := c.entries[cacheKey(pkgID, version, index, want)]
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	if ChunkHash(index, p) != want {
		return nil, false
	}
	out := make([]byte, len(p))
	copy(out, p)
	return out, true
}

// Len reports the number of entries (diagnostics/tests).
func (c *ChunkCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// CorruptForTest flips one byte of the cached entry for (pkgID, version,
// index, want) to simulate external cache storage tampering. It is a test
// helper and returns ErrChunkHash when no such entry exists.
func (c *ChunkCache) CorruptForTest(pkgID string, version uint64, index uint32, want [32]byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey(pkgID, version, index, want)
	p, ok := c.entries[k]
	if !ok {
		return ErrChunkHash
	}
	p[0] ^= 0xFF
	return nil
}
