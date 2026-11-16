package flowspec

import (
	"net/netip"
	"sort"
	"sync"
)

// MemoryRIB is an in-memory RIB reference implementation storing one best
// route per prefix. It is safe for concurrent use.
type MemoryRIB struct {
	mu     sync.RWMutex
	routes map[string]UnicastRoute
}

// NewMemoryRIB creates an empty MemoryRIB.
func NewMemoryRIB() *MemoryRIB {
	return &MemoryRIB{routes: make(map[string]UnicastRoute)}
}

// Set stores or replaces the best route for route.Prefix.
func (r *MemoryRIB) Set(route UnicastRoute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[route.Prefix.String()] = route
}

// Delete removes the route for prefix. It reports whether one existed.
func (r *MemoryRIB) Delete(prefix netip.Prefix) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routes[prefix.String()]; !ok {
		return false
	}
	delete(r.routes, prefix.String())
	return true
}

// BestRoute implements RIB with longest-prefix matching.
func (r *MemoryRIB) BestRoute(prefix netip.Prefix) (UnicastRoute, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var best UnicastRoute
	found := false
	bestBits := -1
	for _, route := range r.routes {
		if route.Prefix.Addr().BitLen() != prefix.Addr().BitLen() ||
			!route.Prefix.Contains(prefix.Addr()) {
			continue
		}
		if route.Prefix.Bits() > bestBits {
			best = route
			bestBits = route.Prefix.Bits()
			found = true
		}
	}
	return best, found
}

// MoreSpecificRoutes implements RIB: every stored prefix strictly contained
// in prefix with a longer prefix length.
func (r *MemoryRIB) MoreSpecificRoutes(prefix netip.Prefix) []UnicastRoute {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []UnicastRoute
	for _, route := range r.routes {
		if route.Prefix.Bits() <= prefix.Bits() ||
			route.Prefix.Addr().BitLen() != prefix.Addr().BitLen() {
			continue
		}
		if prefix.Contains(route.Prefix.Addr()) {
			result = append(result, route)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Prefix.String() < result[j].Prefix.String()
	})
	return result
}
