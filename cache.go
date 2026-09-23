package blockpack

// Cache stores digests of blocks that have already been verified, isolated
// per package version. A cached entry is only honored when the current
// package's own summary commits to the same digest for the same block index
// and the same version, so blocks verified under an old version are never
// reused without a fresh digest cross-check.
type Cache struct {
	entries map[cacheKey]Digest
}

type cacheKey struct {
	version uint32
	index   int
}

func NewCache() *Cache {
	return &Cache{entries: make(map[cacheKey]Digest)}
}

// Store records a successfully verified block digest under the package
// version it was verified for.
func (c *Cache) Store(version uint32, index int, d Digest) {
	c.entries[cacheKey{version, index}] = d
}

// Lookup reports whether the cache holds an entry for exactly this package
// version and block index whose digest equals the digest committed by the
// current package. Any mismatch — different version, different index, or
// different committed digest — is a miss and forces re-verification.
func (c *Cache) Lookup(version uint32, index int, committed Digest) bool {
	d, ok := c.entries[cacheKey{version, index}]
	return ok && d == committed
}
