package pdbind

import (
	"net/netip"
	"sync"
)

// Route models a routing table entry for a delegated prefix: traffic for
// Prefix is forwarded to NextHop via the relay/link identified by LinkAddr.
type Route struct {
	Prefix  netip.Prefix
	NextHop netip.Addr
	Link    netip.Addr
}

// RouteTable is the routing integration point. The server's packet handler is
// expected to provide an implementation that talks to the real FIB; tests use
// MemoryRouteTable.
type RouteTable interface {
	Install(route Route) error
	Remove(prefix netip.Prefix) error
}

// MemoryRouteTable is an in-memory RouteTable safe for concurrent use. It
// also records the chronological operations for assertions in tests.
type MemoryRouteTable struct {
	mu      sync.Mutex
	routes  map[string]Route
	history []routeOp
}

type routeOp struct {
	kind   string
	prefix netip.Prefix
}

// NewMemoryRouteTable creates an empty in-memory route table.
func NewMemoryRouteTable() *MemoryRouteTable {
	return &MemoryRouteTable{routes: make(map[string]Route)}
}

// Install adds or replaces the route for route.Prefix.
func (m *MemoryRouteTable) Install(route Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := route.Prefix.String()
	m.routes[key] = route
	m.history = append(m.history, routeOp{kind: "install", prefix: route.Prefix})
	return nil
}

// Remove deletes the route for prefix; removing an absent prefix is a no-op.
func (m *MemoryRouteTable) Remove(prefix netip.Prefix) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.routes, prefix.String())
	m.history = append(m.history, routeOp{kind: "remove", prefix: prefix})
	return nil
}

// Lookup returns the installed route for prefix and whether it exists.
func (m *MemoryRouteTable) Lookup(prefix netip.Prefix) (Route, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[prefix.String()]
	return r, ok
}

// Count returns the number of currently installed routes.
func (m *MemoryRouteTable) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.routes)
}

// History returns a copy of the recorded install/remove operations.
func (m *MemoryRouteTable) History() []routeOp {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]routeOp, len(m.history))
	copy(out, m.history)
	return out
}

// Snapshot returns a copy of the currently installed routes keyed by prefix.
func (m *MemoryRouteTable) Snapshot() map[string]Route {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Route, len(m.routes))
	for k, v := range m.routes {
		out[k] = v
	}
	return out
}
