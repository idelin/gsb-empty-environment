package certcache

import (
	"fmt"
	"strings"
)

// Cache stores query certificates. The two certificate types are managed in
// separate stores with separate reverse indexes, so graph mutations can
// apply different invalidation policies to each type.
type Cache struct {
	reachCerts  map[Pair]*ReachCert
	reachByEdge map[EdgeKey]map[Pair]struct{}
	spCerts     map[Pair]*SPCert
	spByEdge    map[EdgeKey]map[Pair]struct{}
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{
		reachCerts:  make(map[Pair]*ReachCert),
		reachByEdge: make(map[EdgeKey]map[Pair]struct{}),
		spCerts:     make(map[Pair]*SPCert),
		spByEdge:    make(map[EdgeKey]map[Pair]struct{}),
	}
}

// ReachSize reports how many reachability certificates are cached.
func (c *Cache) ReachSize() int { return len(c.reachCerts) }

// SPSize reports how many shortest-path certificates are cached.
func (c *Cache) SPSize() int { return len(c.spCerts) }

func (c *Cache) putReach(cert *ReachCert) {
	c.dropReach(cert.Pair)
	c.reachCerts[cert.Pair] = cert
	for key := range cert.Deps {
		addIndex(c.reachByEdge, key, cert.Pair)
	}
}

func (c *Cache) putSP(cert *SPCert) {
	c.dropSP(cert.Pair)
	c.spCerts[cert.Pair] = cert
	for key := range cert.Deps {
		addIndex(c.spByEdge, key, cert.Pair)
	}
}

func (c *Cache) dropReach(p Pair) {
	cert, ok := c.reachCerts[p]
	if !ok {
		return
	}
	for key := range cert.Deps {
		delIndex(c.reachByEdge, key, p)
	}
	delete(c.reachCerts, p)
}

func (c *Cache) dropSP(p Pair) {
	cert, ok := c.spCerts[p]
	if !ok {
		return
	}
	for key := range cert.Deps {
		delIndex(c.spByEdge, key, p)
	}
	delete(c.spCerts, p)
}

// getReach returns the cached certificate if every recorded dependency
// version still matches the graph; stale entries are evicted lazily.
func (c *Cache) getReach(g *Graph, p Pair) (*ReachCert, bool) {
	cert, ok := c.reachCerts[p]
	if !ok {
		return nil, false
	}
	if !validDeps(g, cert.Deps) {
		c.dropReach(p)
		return nil, false
	}
	return cert, true
}

// getSP returns the cached certificate if no edge was added since it was
// recorded and every dependency version still matches.
func (c *Cache) getSP(g *Graph, p Pair) (*SPCert, bool) {
	cert, ok := c.spCerts[p]
	if !ok {
		return nil, false
	}
	if cert.Adds != g.adds || !validDeps(g, cert.Deps) {
		c.dropSP(p)
		return nil, false
	}
	return cert, true
}

// validDeps reports whether every recorded dependency version still matches
// the edge's current version. A generation mismatch (version wraparound)
// rejects the certificate even when the wrapped value coincides.
func validDeps(g *Graph, deps map[EdgeKey]Version) bool {
	for key, v := range deps {
		e, ok := g.edges[key]
		if !ok || e.Version != v {
			return false
		}
	}
	return true
}

// onEdgeChanged and onEdgeRemoved invalidate exactly the certificates whose
// dependency sets contain the edge, in both certificate stores.
func (c *Cache) onEdgeChanged(key EdgeKey) { c.invalidateByEdge(key) }

func (c *Cache) onEdgeRemoved(key EdgeKey) { c.invalidateByEdge(key) }

func (c *Cache) invalidateByEdge(key EdgeKey) {
	for p := range c.reachByEdge[key] {
		c.dropReach(p)
	}
	for p := range c.spByEdge[key] {
		c.dropSP(p)
	}
}

// onEdgeAdded invalidates all shortest-path certificates (a new edge may
// open a shorter route) but leaves reachability certificates untouched:
// existing paths remain valid under additions.
func (c *Cache) onEdgeAdded(key EdgeKey) {
	for p := range c.spCerts {
		c.dropSP(p)
	}
}

// onGenerationBump flushes everything: after a version wraparound no
// recorded version can be trusted.
func (c *Cache) onGenerationBump() {
	c.reachCerts = make(map[Pair]*ReachCert)
	c.reachByEdge = make(map[EdgeKey]map[Pair]struct{})
	c.spCerts = make(map[Pair]*SPCert)
	c.spByEdge = make(map[EdgeKey]map[Pair]struct{})
}

// VerifyAll fully re-verifies every cached certificate against the current
// graph, cross-checking that targeted invalidation left no stale
// certificates behind.
func (c *Cache) VerifyAll(g *Graph) error {
	var errs []string
	for p, cert := range c.reachCerts {
		if !VerifyReachCert(g, cert) {
			errs = append(errs, fmt.Sprintf("stale reachability certificate for (%d,%d)", p.From, p.To))
		}
	}
	for p, cert := range c.spCerts {
		if !VerifySPCert(g, cert) {
			errs = append(errs, fmt.Sprintf("stale shortest-path certificate for (%d,%d)", p.From, p.To))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("certcache: full re-verification failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

func addIndex(idx map[EdgeKey]map[Pair]struct{}, key EdgeKey, p Pair) {
	m, ok := idx[key]
	if !ok {
		m = make(map[Pair]struct{})
		idx[key] = m
	}
	m[p] = struct{}{}
}

func delIndex(idx map[EdgeKey]map[Pair]struct{}, key EdgeKey, p Pair) {
	m, ok := idx[key]
	if !ok {
		return
	}
	delete(m, p)
	if len(m) == 0 {
		delete(idx, key)
	}
}
