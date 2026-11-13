package netsec

import "sync"

// Graph is the root handle returned by Decode. Closing it releases every
// successfully constructed own-reachable object exactly once, in reverse
// construction order, without recursively invoking destructors for children.
type Graph struct {
	rootID    ID
	rootValue any
	built     []builtNode
	destroy   Destructor

	mu     sync.Mutex
	closed bool
}

// RootID returns the decoded root object's stable ID.
func (g *Graph) RootID() ID {
	if g == nil {
		return 0
	}
	return g.rootID
}

// RootValue returns the value produced by the root object's constructor.
func (g *Graph) RootValue() any {
	if g == nil {
		return nil
	}
	return g.rootValue
}

// Root is an alias for RootValue.
func (g *Graph) Root() any {
	return g.RootValue()
}

// NodeCount returns the number of objects constructed for this graph.
func (g *Graph) NodeCount() int {
	if g == nil {
		return 0
	}
	return len(g.built)
}

// Close destroys constructed objects once in reverse construction order.
// Repeated and nil-receiver calls are safe. Destructors do not return errors,
// so Close always returns nil and implements io.Closer. A destructor is never
// called for one object as a side effect of destroying another.
func (g *Graph) Close() error {
	if g == nil {
		return nil
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	built := g.built
	destroy := g.destroy
	g.mu.Unlock()

	for i := len(built) - 1; i >= 0; i-- {
		destroy(built[i].id, built[i].typeName, built[i].value)
	}
	return nil
}
