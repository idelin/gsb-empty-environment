package netsec

import (
	"fmt"
	"sort"
	"sync"
)

// WriteProvenance ties an output path to the exact callback action that last
// produced it.
type WriteProvenance struct {
	Script  string   `json:"script"`
	Process []string `json:"process,omitempty"`
	Action  Action   `json:"action"`
	Event   int      `json:"event"`
}

// CacheEntry is one immutable successful package output snapshot.
type CacheEntry struct {
	Files      map[string][]byte
	Provenance map[string]WriteProvenance
}

// Cache stores successful package outputs by content digest. All values are
// deep-copied at Put and Get boundaries.
type Cache struct {
	mu      sync.RWMutex
	entries map[Digest]CacheEntry
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{entries: make(map[Digest]CacheEntry)}
}

// Put stores a successful output snapshot.
func (c *Cache) Put(digest Digest, files map[string][]byte, provenance map[string]WriteProvenance) error {
	if c == nil {
		return fmt.Errorf("netsec: nil cache")
	}
	if err := digest.Validate(); err != nil {
		return err
	}
	copiedFiles := make(map[string][]byte, len(files))
	for path, data := range files {
		if err := validatePath(path); err != nil {
			return err
		}
		copiedFiles[path] = append([]byte(nil), data...)
	}
	copiedProvenance := copyProvenance(provenance)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[Digest]CacheEntry)
	}
	c.entries[digest] = CacheEntry{Files: copiedFiles, Provenance: copiedProvenance}
	return nil
}

// Get returns a defensive copy of a cache entry.
func (c *Cache) Get(digest Digest) (CacheEntry, bool) {
	if c == nil {
		return CacheEntry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[digest]
	if !ok {
		return CacheEntry{}, false
	}
	files := make(map[string][]byte, len(entry.Files))
	for path, data := range entry.Files {
		files[path] = append([]byte(nil), data...)
	}
	return CacheEntry{Files: files, Provenance: copyProvenance(entry.Provenance)}, true
}

// Has reports whether a digest has a cached successful output.
func (c *Cache) Has(digest Digest) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.entries[digest]
	return ok
}

// Remove invalidates one package cache entry without touching unrelated ones.
func (c *Cache) Remove(digest Digest) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, digest)
}

// Digests returns cached package digests in stable order.
func (c *Cache) Digests() []Digest {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Digest, 0, len(c.entries))
	for digest := range c.entries {
		out = append(out, digest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func copyProvenance(in map[string]WriteProvenance) map[string]WriteProvenance {
	out := make(map[string]WriteProvenance, len(in))
	for path, provenance := range in {
		provenance.Process = append([]string(nil), provenance.Process...)
		out[path] = provenance
	}
	return out
}
