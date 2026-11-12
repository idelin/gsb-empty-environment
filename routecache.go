package netsec

import (
	"net/netip"
	"sync"
)

// RouteCache records the next-hop gateway currently in use for each
// destination. It is safe for concurrent use.
type RouteCache struct {
	mu      sync.RWMutex
	nextHop map[netip.Addr]netip.Addr
}

func NewRouteCache() *RouteCache {
	return &RouteCache{nextHop: make(map[netip.Addr]netip.Addr)}
}

// Lookup returns the next-hop gateway for dst.
func (c *RouteCache) Lookup(dst netip.Addr) (netip.Addr, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	gw, ok := c.nextHop[dst]
	return gw, ok
}

// Set installs or replaces the next-hop gateway for dst. It is used both
// for statically configured routes and for accepted ICMP redirects.
func (c *RouteCache) Set(dst, gateway netip.Addr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextHop[dst] = gateway
}
