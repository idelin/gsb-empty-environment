package paxos

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Network is an in-memory message transport between nodes. It models
// network partitions and per-link message drops, which makes it suitable
// for deterministic failure-injection tests. All deliveries are
// synchronous: a Send* call invokes the receiver's handler directly.
type Network struct {
	mu         sync.RWMutex
	nodes      map[int]*Node
	blocked    map[[2]int]bool // directed blocked links
	commitDrop map[[2]int]bool // directed links that drop only Commit messages

	prepares atomic.Int64
	accepts  atomic.Int64
	commits  atomic.Int64
	syncs    atomic.Int64
}

// NewNetwork returns an empty transport.
func NewNetwork() *Network {
	return &Network{
		nodes:      make(map[int]*Node),
		blocked:    make(map[[2]int]bool),
		commitDrop: make(map[[2]int]bool),
	}
}

// Add registers a node.
func (n *Network) Add(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.id] = node
}

// Peers returns the sorted IDs of all registered nodes.
func (n *Network) Peers() []int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	ids := make([]int, 0, len(n.nodes))
	for id := range n.nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// Size returns the number of registered nodes.
func (n *Network) Size() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.nodes)
}

// Majority returns the quorum size.
func (n *Network) Majority() int { return n.Size()/2 + 1 }

// Block drops all messages from a to b (directed).
func (n *Network) Block(a, b int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked[[2]int{a, b}] = true
}

// Unblock reopens the directed link from a to b.
func (n *Network) Unblock(a, b int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.blocked, [2]int{a, b})
}

// DropCommitsTo makes the directed link a->b drop only Commit messages
// while Accept/Prepare/Sync still flow. It models a leader whose commit
// notifications are lost right after a majority accepted an entry.
func (n *Network) DropCommitsTo(a, b int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.commitDrop[[2]int{a, b}] = true
}

// Heal removes every partition and drop rule.
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = make(map[[2]int]bool)
	n.commitDrop = make(map[[2]int]bool)
}

// Partition splits the cluster into the given groups: links inside a group
// stay open, links across groups are blocked in both directions.
func (n *Network) Partition(groups ...[]int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = make(map[[2]int]bool)
	groupOf := make(map[int]int)
	for gi, g := range groups {
		for _, id := range g {
			groupOf[id] = gi
		}
	}
	for a := range n.nodes {
		for b := range n.nodes {
			if a != b && groupOf[a] != groupOf[b] {
				n.blocked[[2]int{a, b}] = true
			}
		}
	}
}

func (n *Network) deliverable(from, to int) bool {
	if from == to {
		return true
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return !n.blocked[[2]int{from, to}]
}

// Prepare sends a Phase 1 request. ok=false means the link is down.
func (n *Network) Prepare(from, to int, req PrepareReq) (resp PromiseResp, ok bool) {
	n.prepares.Add(1)
	if !n.deliverable(from, to) {
		return PromiseResp{}, false
	}
	n.mu.RLock()
	peer := n.nodes[to]
	n.mu.RUnlock()
	return peer.HandlePrepare(req), true
}

// Accept sends a Phase 2 request. ok=false means the link is down.
func (n *Network) Accept(from, to int, req AcceptReq) (resp AcceptedResp, ok bool) {
	n.accepts.Add(1)
	if !n.deliverable(from, to) {
		return AcceptedResp{}, false
	}
	n.mu.RLock()
	peer := n.nodes[to]
	n.mu.RUnlock()
	return peer.HandleAccept(req), true
}

// Commit sends a commit notification. ok=false means it was dropped.
func (n *Network) Commit(from, to int, req CommitReq) bool {
	n.commits.Add(1)
	if from == to {
		return true
	}
	n.mu.RLock()
	dropped := n.blocked[[2]int{from, to}] || n.commitDrop[[2]int{from, to}]
	peer := n.nodes[to]
	n.mu.RUnlock()
	if dropped {
		return false
	}
	peer.HandleCommit(req)
	return true
}

// Sync pulls committed entries from a peer. ok=false means the link is down.
func (n *Network) Sync(from, to int, req SyncReq) (resp SyncResp, ok bool) {
	n.syncs.Add(1)
	if !n.deliverable(from, to) {
		return SyncResp{}, false
	}
	n.mu.RLock()
	peer := n.nodes[to]
	n.mu.RUnlock()
	return peer.HandleSync(req), true
}

// Message counters, used by tests to assert protocol behaviour such as
// "no Phase 1 traffic while the lease holds".

func (n *Network) PrepareCount() int64 { return n.prepares.Load() }
func (n *Network) AcceptCount() int64  { return n.accepts.Load() }
func (n *Network) CommitCount() int64  { return n.commits.Load() }
func (n *Network) SyncCount() int64    { return n.syncs.Load() }
