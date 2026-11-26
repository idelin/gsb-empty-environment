package plancache

import "sync"

// PooledConnection models one physical pooled connection that can be
// re-bound to different principals over its lifetime.
type PooledConnection struct {
	mu        sync.Mutex
	executor  *Executor
	principal PrincipalID
}

// ConnectionPool hands out session-bound connections sharing one GLOBAL
// cache while keeping per-connection SESSION caches.
type ConnectionPool struct {
	cat      Catalog
	global   *PlanCache
	observer Observer
}

// NewConnectionPool creates a pool over the catalog with a shared GLOBAL
// cache.
func NewConnectionPool(cat Catalog, observer Observer) *ConnectionPool {
	return &ConnectionPool{
		cat:      cat,
		global:   NewPlanCache(GLOBAL),
		observer: observer,
	}
}

// Acquire binds a fresh pooled connection to a principal. The connection
// gets its own empty SESSION cache.
func (p *ConnectionPool) Acquire(principal PrincipalID) *PooledConnection {
	session := NewPlanCache(SESSION)
	return &PooledConnection{
		executor:  NewExecutor(p.cat, session, p.global, p.observer),
		principal: principal,
	}
}

// Executor exposes the connection's executor.
func (c *PooledConnection) Executor() *Executor {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.executor
}

// Principal reports the currently bound principal.
func (c *PooledConnection) Principal() PrincipalID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.principal
}

// SwitchPrincipal re-binds the connection to a new principal. Every
// session-local handle is invalidated immediately and the SESSION cache is
// cleared. GLOBAL entries survive and are only usable when the new
// principal's own full key matches.
func (c *PooledConnection) SwitchPrincipal(p PrincipalID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p == c.principal {
		return
	}
	c.executor.InvalidateSession()
	c.principal = p
}

// GlobalCache exposes the shared cache for assertions/seeding.
func (p *ConnectionPool) GlobalCache() *PlanCache { return p.global }
