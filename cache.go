package certcache

import "sync"

// Cache stores query certificates, managed separately per certificate
// type, and invalidates them surgically as the graph mutates:
//
//   - edge removal or weight change: only certificates that depend on
//     that exact edge are dropped (both types);
//   - edge insertion: all shortest-path certificates are dropped (a
//     shorter path may now exist) while reachability certificates stay
//     valid (their witness paths still exist).
type Cache struct {
	g *Graph

	mu       sync.RWMutex
	reach    map[Query]*Certificate
	shortest map[Query]*Certificate
}

// NewCache creates an empty certificate cache over g.
func NewCache(g *Graph) *Cache {
	return &Cache{
		g:        g,
		reach:    make(map[Query]*Certificate),
		shortest: make(map[Query]*Certificate),
	}
}

// store returns the per-kind certificate map.
func (c *Cache) store(kind CertKind) map[Query]*Certificate {
	if kind == KindShortestPath {
		return c.shortest
	}
	return c.reach
}

// AddEdge inserts an edge and invalidates every shortest-path
// certificate. Reachability certificates are unaffected.
func (c *Cache) AddEdge(from, to string, weight int64) error {
	if err := c.g.AddEdge(from, to, weight); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shortest = make(map[Query]*Certificate)
	return nil
}

// RemoveEdge deletes an edge and drops only the certificates (of either
// type) that depend on it.
func (c *Cache) RemoveEdge(from, to string) bool {
	if !c.g.RemoveEdge(from, to) {
		return false
	}
	c.invalidateDependents(EdgeKey{From: from, To: to})
	return true
}

// SetWeight changes an edge weight (bumping its version) and drops the
// certificates that depend on it. A weight decrease may additionally
// create shorter paths through this edge, so — like an insertion — it
// also drops all shortest-path certificates. Reachability certificates
// are only affected when they depend on the edge itself.
func (c *Cache) SetWeight(from, to string, weight int64) error {
	old := c.g.Edge(from, to)
	if err := c.g.SetWeight(from, to, weight); err != nil {
		return err
	}
	c.invalidateDependents(EdgeKey{From: from, To: to})
	if old != nil && weight < old.Weight {
		c.mu.Lock()
		c.shortest = make(map[Query]*Certificate)
		c.mu.Unlock()
	}
	return nil
}

// invalidateDependents drops certificates relying on key, per type.
func (c *Cache) invalidateDependents(key EdgeKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, store := range []map[Query]*Certificate{c.reach, c.shortest} {
		for q, cert := range store {
			if cert.dependsOn(key) {
				delete(store, q)
			}
		}
	}
}

// Reachable answers a reachability query, serving and caching a
// reachability certificate. Negative answers are not certified (an edge
// insertion could flip them) and are recomputed every time.
func (c *Cache) Reachable(from, to string) (bool, *Certificate) {
	q := Query{From: from, To: to}
	c.mu.RLock()
	cert, ok := c.reach[q]
	c.mu.RUnlock()
	if ok && Validate(c.g, cert) {
		return true, cert
	}

	reachable, fresh := FullReachable(c.g, from, to)
	if !reachable {
		return false, nil
	}
	c.mu.Lock()
	c.reach[q] = fresh
	c.mu.Unlock()
	return true, fresh
}

// Shortest answers a shortest-path query, serving and caching a
// shortest-path certificate. Unreachable targets are not certified.
func (c *Cache) Shortest(from, to string) (int64, bool, *Certificate) {
	q := Query{From: from, To: to}
	c.mu.RLock()
	cert, ok := c.shortest[q]
	c.mu.RUnlock()
	if ok && Validate(c.g, cert) {
		return cert.Distance, true, cert
	}

	dist, reachable, fresh := FullShortest(c.g, from, to)
	if !reachable {
		return 0, false, nil
	}
	c.mu.Lock()
	c.shortest[q] = fresh
	c.mu.Unlock()
	return dist, true, fresh
}

// Cached returns the currently cached certificate of a kind for a query,
// or nil. Useful for inspecting invalidation behavior.
func (c *Cache) Cached(kind CertKind, from, to string) *Certificate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.store(kind)[Query{From: from, To: to}]
}

// Len reports how many certificates of a kind are currently cached.
func (c *Cache) Len(kind CertKind) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.store(kind))
}
