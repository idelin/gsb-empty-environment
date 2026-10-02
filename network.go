package multipaxos

import "sync"

// Network is an in-memory message fabric with partition simulation and
// instrumentation. Delivery is synchronous (in the caller's goroutine), which
// keeps the protocol deterministic and race-detector friendly.
type Network struct {
	mu     sync.Mutex
	nodes  map[int]*Node
	ids    []int
	cut    map[[2]int]bool
	closed bool

	prepares int
	accepts  int
	commits  int
}

// NewNetwork creates an empty network.
func NewNetwork() *Network {
	return &Network{
		nodes: make(map[int]*Node),
		cut:   make(map[[2]int]bool),
	}
}

// Register attaches a node.
func (n *Network) Register(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.id] = node
	n.ids = append(n.ids, node.id)
}

// Size returns the number of registered nodes.
func (n *Network) Size() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.ids)
}

// IDs returns a copy of all node ids.
func (n *Network) IDs() []int {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]int, len(n.ids))
	copy(out, n.ids)
	return out
}

// Partition cuts all traffic between different groups. Nodes not listed in
// any group are treated as members of the first group.
func (n *Network) Partition(groups ...[]int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cut = make(map[[2]int]bool)
	grp := make(map[int]int)
	for gi, g := range groups {
		for _, id := range g {
			grp[id] = gi
		}
	}
	for _, a := range n.ids {
		for _, b := range n.ids {
			if a != b && grp[a] != grp[b] {
				n.cut[[2]int{a, b}] = true
			}
		}
	}
}

// Heal removes all partitions.
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cut = make(map[[2]int]bool)
}

// Close shuts the fabric down; all subsequent sends fail.
func (n *Network) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = true
}

// Stats returns (prepares, accepts, commits) delivered so far.
func (n *Network) Stats() (int, int, int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.prepares, n.accepts, n.commits
}

// ResetStats zeroes the instrumentation counters.
func (n *Network) ResetStats() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.prepares, n.accepts, n.commits = 0, 0, 0
}

func (n *Network) deliverable(from, to int) (*Node, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || n.cut[[2]int{from, to}] {
		return nil, false
	}
	node, ok := n.nodes[to]
	return node, ok
}

// Prepare delivers a Phase 1a message; ok=false means the link was cut.
func (n *Network) Prepare(from, to int, req PrepareReq) (PrepareResp, bool) {
	node, ok := n.deliverable(from, to)
	if !ok {
		return PrepareResp{}, false
	}
	n.mu.Lock()
	n.prepares++
	n.mu.Unlock()
	return node.handlePrepare(req), true
}

// Accept delivers a Phase 2a message.
func (n *Network) Accept(from, to int, req AcceptReq) (AcceptResp, bool) {
	node, ok := n.deliverable(from, to)
	if !ok {
		return AcceptResp{}, false
	}
	n.mu.Lock()
	n.accepts++
	n.mu.Unlock()
	return node.handleAccept(req), true
}

// BroadcastCommit sends the committed prefix to every reachable peer.
func (n *Network) BroadcastCommit(from int, req CommitReq) {
	for _, id := range n.IDs() {
		if id == from {
			continue
		}
		node, ok := n.deliverable(from, id)
		if !ok {
			continue
		}
		n.mu.Lock()
		n.commits++
		n.mu.Unlock()
		node.handleCommit(req)
	}
}
