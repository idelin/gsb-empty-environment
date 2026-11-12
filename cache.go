package taskpkg

import "sync"

// EntryState classifies a cache item. Only Authoritative entries may be used
// to answer new queries. Diagnostic entries are retained for forensics only.
type EntryState int

const (
	// Authoritative means the answer was valid and its signer is still
	// retained by the current trust generation.
	Authoritative EntryState = iota
	// Diagnostic means the signature was valid at the pinned generation, but
	// the signer has since been revoked and must never authorize a new query.
	Diagnostic
)

func (s EntryState) String() string {
	switch s {
	case Authoritative:
		return "authoritative"
	case Diagnostic:
		return "diagnostic"
	default:
		return "unknown"
	}
}

// Entry is one cache record. Every record carries the trust path observed at
// publish time.
type Entry struct {
	State  EntryState
	Data   []byte
	Path   TrustPath
	Reason string
}

// CacheKey identifies a cache slot independent of trust generation.
type CacheKey struct {
	Zone  string
	QName string
}

// Cache stores authoritative and diagnostic records. Diagnostic records are
// kept in a separate map so Lookup can never return them to a new query.
type Cache struct {
	mu           sync.RWMutex
	auth         map[CacheKey]*Entry
	diagnostic   map[CacheKey]*Entry
	publishCount int
}

// NewCache creates an empty cache.
func NewCache() *Cache {
	return &Cache{
		auth:       make(map[CacheKey]*Entry),
		diagnostic: make(map[CacheKey]*Entry),
	}
}

// Publish stores an entry. Authoritative entries replace any diagnostic
// entry for the same slot; diagnostic entries never overwrite authoritative
// ones and are kept out of the lookup path.
func (c *Cache) Publish(key CacheKey, entry *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publishCount++
	stored := cloneEntry(entry)
	switch entry.State {
	case Authoritative:
		delete(c.diagnostic, key)
		c.auth[key] = stored
	case Diagnostic:
		if _, ok := c.auth[key]; !ok {
			c.diagnostic[key] = stored
		}
	}
}

// Lookup returns only authoritative entries; revoked-key answers are never
// visible here and therefore cannot authorize new queries.
func (c *Cache) Lookup(key CacheKey) (*Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.auth[key]
	if !ok {
		return nil, false
	}
	return cloneEntry(entry), true
}

// Diagnostic returns a retained revoked-key answer for forensic inspection.
func (c *Cache) Diagnostic(key CacheKey) (*Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.diagnostic[key]
	if !ok {
		return nil, false
	}
	return cloneEntry(entry), true
}

// AuthCount and DiagnosticCount report current record counts.
func (c *Cache) AuthCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.auth)
}

// DiagnosticCount reports how many revoked-key answers are retained.
func (c *Cache) DiagnosticCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.diagnostic)
}

// PublishCount reports total publish calls, including diagnostic ones.
func (c *Cache) PublishCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.publishCount
}

func cloneEntry(in *Entry) *Entry {
	out := *in
	if in.Data != nil {
		out.Data = append([]byte(nil), in.Data...)
	}
	hops := make([]Hop, len(in.Path.Hops))
	copy(hops, in.Path.Hops)
	out.Path.Hops = hops
	return &out
}
