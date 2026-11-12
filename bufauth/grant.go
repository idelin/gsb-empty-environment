package bufauth

import "sync"

// Grant is one node in a derived authorization tree. A grant is immutable
// except for its live flag, which is flipped by cascade revocation.
type Grant struct {
	mu       sync.RWMutex
	id       int64
	bufferID string
	perms    Perm
	parent   *Grant
	children []*Grant
	live     bool
}

// ID returns the authority-wide unique grant identifier.
func (g *Grant) ID() int64 {
	return g.id
}

// BufferID returns the identifier of the buffer the grant applies to.
func (g *Grant) BufferID() string {
	return g.bufferID
}

// Perms returns the permission mask captured when the grant was issued.
func (g *Grant) Perms() Perm {
	return g.perms
}

// ParentID returns the id of the upstream grant, or zero for a root grant.
func (g *Grant) ParentID() int64 {
	if g.parent == nil {
		return 0
	}
	return g.parent.id
}

// Live reports whether the grant is still valid. A false result means this
// grant or one of its ancestors was revoked.
func (g *Grant) Live() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.live
}

func (g *Grant) markRevoked() {
	g.mu.Lock()
	g.live = false
	g.mu.Unlock()
}

// subtree walks the grant and every descendant, invoking fn on each node.
func (g *Grant) subtree(fn func(*Grant)) {
	fn(g)
	for _, child := range g.children {
		child.subtree(fn)
	}
}
