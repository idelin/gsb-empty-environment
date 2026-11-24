package readproof

import (
	"fmt"
	"sync"
)

// NodeSource supplies nodes by claimed identity.  Implementations must be safe
// for concurrent reads if Callers mutate the catalog concurrently.
type NodeSource interface {
	Node(Digest) (Node, bool)
}

// Catalog is an out-of-order manifest node store keyed by canonical digest.
// Adding a node does not require its children to be present.
type Catalog struct {
	mu    sync.RWMutex
	nodes map[Digest]Node
}

// Manifest and Store are descriptive aliases for Catalog.
type Manifest = Catalog
type Store = Catalog

// NewCatalog constructs an empty manifest catalog.
func NewCatalog() *Catalog {
	return &Catalog{nodes: make(map[Digest]Node)}
}

// NewManifest constructs an empty manifest catalog.
func NewManifest() *Catalog {
	return NewCatalog()
}

// Add canonically hashes n, stores it, and returns its identity.
func (c *Catalog) Add(n Node) (Digest, error) {
	clone := cloneNode(n)
	id, err := clone.Digest()
	if err != nil {
		return Digest{}, err
	}
	c.mu.Lock()
	if c.nodes == nil {
		c.nodes = make(map[Digest]Node)
	}
	c.nodes[id] = clone
	c.mu.Unlock()
	return id, nil
}

// Put verifies that n hashes to id before storing it.
func (c *Catalog) Put(id Digest, n Node) error {
	clone := cloneNode(n)
	got, err := clone.Digest()
	if err != nil {
		return err
	}
	if got != id {
		return fmt.Errorf("%w: claimed %s, computed %s", ErrIdentityMismatch, id, got)
	}
	c.mu.Lock()
	if c.nodes == nil {
		c.nodes = make(map[Digest]Node)
	}
	c.nodes[id] = clone
	c.mu.Unlock()
	return nil
}

// AddNode is an alias for Put.
func (c *Catalog) AddNode(id Digest, n Node) error { return c.Put(id, n) }

// AddWithDigest is an alias for Put.
func (c *Catalog) AddWithDigest(id Digest, n Node) error { return c.Put(id, n) }

// Set is an alias for Put.
func (c *Catalog) Set(id Digest, n Node) error { return c.Put(id, n) }

// Node returns a defensive copy of a stored node.
func (c *Catalog) Node(id Digest) (Node, bool) {
	c.mu.RLock()
	n, ok := c.nodes[id]
	c.mu.RUnlock()
	if !ok {
		return Node{}, false
	}
	return cloneNode(n), true
}

// Get is an alias for Node.
func (c *Catalog) Get(id Digest) (Node, bool) { return c.Node(id) }

// Len returns the number of unique nodes currently indexed.
func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.nodes)
}

func cloneNode(n Node) Node {
	return Node{
		Kind:     n.Kind,
		Leaf:     n.Leaf,
		Children: append([]Child(nil), n.Children...),
	}
}
