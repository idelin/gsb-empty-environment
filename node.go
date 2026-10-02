package main

// Node is a simulated cluster member: a CRDT store plus its local view of
// every peer's vector clock, exchanged via gossip.
type Node struct {
	ID    string
	Store *Store
	known map[string]VectorClock // peer clocks as known locally (incl. self)
}

func NewNode(id string) *Node {
	n := &Node{ID: id, Store: NewStore(id), known: map[string]VectorClock{}}
	n.known[id] = n.Store.Clock()
	return n
}

// Gossip performs a symmetric state exchange between two nodes:
// entries, own clocks, and knowledge of third-party clocks.
func (n *Node) Gossip(m *Node) {
	for _, e := range n.Store.Entries() {
		m.Store.apply(e)
	}
	for _, e := range m.Store.Entries() {
		n.Store.apply(e)
	}
	n.Store.MergeClock(m.Store.Clock())
	m.Store.MergeClock(n.Store.Clock())
	n.learn(m.ID, m.Store.Clock())
	m.learn(n.ID, n.Store.Clock())
	for id, vc := range n.known {
		m.learn(id, vc)
	}
	for id, vc := range m.known {
		n.learn(id, vc)
	}
}

func (n *Node) learn(id string, vc VectorClock) {
	known, ok := n.known[id]
	if !ok {
		known = VectorClock{}
		n.known[id] = known
	}
	known.Merge(vc)
}

// Frontier is this node's local view of the causal stability frontier:
// the component-wise minimum over all clocks it knows (including itself).
func (n *Node) Frontier() VectorClock {
	n.known[n.ID] = n.Store.Clock()
	clocks := make([]VectorClock, 0, len(n.known))
	for _, vc := range n.known {
		clocks = append(clocks, vc)
	}
	return Frontier(clocks...)
}

// StableAt reports whether this node has confirmed that every one of the
// `peers` cluster members has advanced its own clock to at least
// `threshold`, i.e. all tombstones with T < threshold are causally stable.
func (n *Node) StableAt(threshold uint64, peers int) bool {
	f := n.Frontier()
	if len(f) < peers {
		return false // some member not even heard of yet
	}
	for _, v := range f {
		if v < threshold {
			return false
		}
	}
	return true
}
