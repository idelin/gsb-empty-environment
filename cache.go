package netsec

import (
	"net/netip"
	"sync"
	"time"
)

// Clock returns the current time. It is an indirection so tests can drive TTL
// expiry deterministically; production code uses time.Now.
type Clock func() time.Time

// DNSResponse is a normalized view of one DNS answer as observed by the DNS
// proxy. Name is the queried name, CNAMEs are the canonical-name chain
// targets in answer order (each hop excluded from the following one), and
// IPs are the terminal A/AAAA addresses. TTL is the smallest resource-record
// TTL that governs the answer, in seconds.
type DNSResponse struct {
	Name   string
	CNAMEs []string
	IPs    []netip.Addr
	TTL    uint32
}

// Cache is the name-to-IP mapping cache shared by the DNS proxy (writes) and
// the policy engine (reads).
//
// An IP is allowed only while at least one unexpired, policy-matched name is
// recorded for it. Every (name, IP) association carries its own deadline, so
// one name expiring never revokes an IP that another live name still covers.
type Cache struct {
	mu       sync.RWMutex
	minTTL   time.Duration
	now      Clock
	exact    map[string]struct{}
	patterns []patternEntry
	// entries maps a normalized name to each IP learned for it and the
	// absolute time at which that association expires.
	entries map[string]map[netip.Addr]time.Time
}

// Option customizes a Cache at construction.
type Option func(*Cache)

// WithMinTTL sets the lower bound applied to every recorded TTL. The cached
// lifetime is the maximum of the answer TTL and this floor.
func WithMinTTL(d time.Duration) Option {
	return func(c *Cache) { c.minTTL = d }
}

// WithClock overrides the time source, primarily for tests.
func WithClock(clock Clock) Option {
	return func(c *Cache) { c.now = clock }
}

// NewCache builds an empty cache.
func NewCache(opts ...Option) *Cache {
	c := &Cache{
		minTTL:  0,
		now:     time.Now,
		exact:   make(map[string]struct{}),
		entries: make(map[string]map[netip.Addr]time.Time),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// AddMatchName registers an exact queried name that policy covers. The match
// is case-insensitive and ignores a trailing root dot.
func (c *Cache) AddMatchName(name string) bool {
	name = normalizeName(name)
	if name == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exact[name] = struct{}{}
	return true
}

// AddMatchPattern registers a glob pattern. '*' only matches characters
// inside a single DNS label and never crosses '.', and patterns must have the
// same number of labels as a name to match. It returns false for malformed
// patterns such as empty labels.
func (c *Cache) AddMatchPattern(pattern string) bool {
	pattern = normalizeName(pattern)
	entry, ok := newPatternEntry(pattern)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, existing := range c.patterns {
		if existing.raw == entry.raw {
			return true
		}
	}
	c.patterns = append(c.patterns, entry)
	return true
}

// nameCovered reports whether name is matched by any configured matchName or
// matchPattern. The caller must not hold c.mu.
func (c *Cache) nameCovered(name string, labels []string) bool {
	if _, ok := c.exact[name]; ok {
		return true
	}
	for _, pattern := range c.patterns {
		if pattern.match(labels) {
			return true
		}
	}
	return false
}

// chainCovered reports whether every hop of the CNAME chain, including the
// queried name itself, is covered by policy. A single uncovered hop means the
// final addresses must not be cached.
func (c *Cache) chainCovered(resp DNSResponse) bool {
	name := normalizeName(resp.Name)
	if name == "" || !c.nameCovered(name, nameLabels(name)) {
		return false
	}
	for _, hop := range resp.CNAMEs {
		target := normalizeName(hop)
		if target == "" || !c.nameCovered(target, nameLabels(target)) {
			return false
		}
	}
	return true
}

// Update records the answer IPs of resp when policy allows it. Nothing is
// recorded when the queried name is not covered, when any CNAME hop escapes
// policy, or when the answer carries no addresses. It returns the IPs that
// were actually recorded.
func (c *Cache) Update(resp DNSResponse) []netip.Addr {
	name := normalizeName(resp.Name)
	if name == "" || len(resp.IPs) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.chainCovered(resp) {
		return nil
	}

	answerTTL := time.Duration(resp.TTL) * time.Second
	ttl := answerTTL
	if c.minTTL > ttl {
		ttl = c.minTTL
	}
	deadline := c.now().Add(ttl)

	byIP, ok := c.entries[name]
	if !ok {
		byIP = make(map[netip.Addr]time.Time)
		c.entries[name] = byIP
	}

	recorded := make([]netip.Addr, 0, len(resp.IPs))
	seen := make(map[netip.Addr]struct{}, len(resp.IPs))
	for _, ip := range resp.IPs {
		if !ip.IsValid() {
			continue
		}
		if _, dup := seen[ip]; dup {
			continue
		}
		seen[ip] = struct{}{}
		// A re-answer refreshes the deadline even if an association exists.
		byIP[ip] = deadline
		recorded = append(recorded, ip)
	}
	return recorded
}

// sweepLocked deletes every expired (name, IP) association and removes name
// buckets that became empty, which is what revokes an IP once all of its
// names have expired.
func (c *Cache) sweepLocked(now time.Time) {
	for name, byIP := range c.entries {
		for ip, deadline := range byIP {
			if !deadline.After(now) {
				delete(byIP, ip)
			}
		}
		if len(byIP) == 0 {
			delete(c.entries, name)
		}
	}
}

// Allowed reports whether ip is currently permitted: at least one unexpired
// name matched by policy still maps to it. Expired associations are reaped
// before the decision.
func (c *Cache) Allowed(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.sweepLocked(now)

	for _, byIP := range c.entries {
		if deadline, ok := byIP[ip]; ok && deadline.After(now) {
			return true
		}
	}
	return false
}

// NamesForIP returns the currently live, policy-matched names that map to ip.
func (c *Cache) NamesForIP(ip netip.Addr) []string {
	if !ip.IsValid() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.sweepLocked(now)

	var names []string
	for name, byIP := range c.entries {
		if deadline, ok := byIP[ip]; ok && deadline.After(now) {
			names = append(names, name)
		}
	}
	return names
}

// Len returns the number of live (name, IP) associations after expiry
// reaping. It is primarily intended for diagnostics and tests.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sweepLocked(c.now())

	total := 0
	for _, byIP := range c.entries {
		total += len(byIP)
	}
	return total
}

// GC eagerly reaps expired associations. Reads already sweep lazily, so this
// is optional for callers that want to bound memory between policy lookups.
func (c *Cache) GC() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepLocked(c.now())
}
