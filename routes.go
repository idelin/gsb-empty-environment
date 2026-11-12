package netsec

import (
	"net/netip"
	"sync"
)

// Route 是一条委派前缀路由。
type Route struct {
	Prefix  netip.Prefix
	NextHop netip.Addr
}

// RouteTable 记录由委派绑定安装的前缀路由。
type RouteTable struct {
	mu     sync.Mutex
	routes map[netip.Prefix]Route
}

func NewRouteTable() *RouteTable {
	return &RouteTable{routes: make(map[netip.Prefix]Route)}
}

func (t *RouteTable) Install(r Route) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.routes[r.Prefix] = r
}

func (t *RouteTable) Withdraw(prefix netip.Prefix) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.routes, prefix)
}

func (t *RouteTable) Lookup(prefix netip.Prefix) (Route, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.routes[prefix]
	return r, ok
}

func (t *RouteTable) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.routes)
}
