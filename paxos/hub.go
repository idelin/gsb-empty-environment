package paxos

import "sync"

// Hub is an in-memory, synchronous message transport with partition
// control. Delivery is a direct method call, which keeps tests
// deterministic and goroutine-free.
type Hub struct {
	mu      sync.Mutex
	nodes   map[int]*Node
	blocked map[[2]int]bool // directional: from -> to
}

func NewHub() *Hub {
	return &Hub{nodes: map[int]*Node{}, blocked: map[[2]int]bool{}}
}

func (h *Hub) Attach(n *Node) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nodes[n.id] = n
}

// Send delivers a message synchronously. Returns ErrUnreachable when the
// link is cut by a partition or the peer does not exist.
func (h *Hub) Send(from, to int, msg any) (any, error) {
	h.mu.Lock()
	n, ok := h.nodes[to]
	blocked := h.blocked[[2]int{from, to}]
	h.mu.Unlock()
	if !ok || blocked {
		return nil, ErrUnreachable
	}
	switch m := msg.(type) {
	case PrepareReq:
		return n.HandlePrepare(m), nil
	case AcceptReq:
		return n.HandleAccept(m), nil
	case CommitNotice:
		n.HandleCommit(m)
		return nil, nil
	case SnapshotMsg:
		n.HandleSnapshot(m)
		return nil, nil
	default:
		panic("hub: unknown message type")
	}
}

// Partition blocks all traffic between different groups (traffic inside a
// group keeps flowing). Existing blocks are cleared first.
func (h *Hub) Partition(groups ...[]int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blocked = map[[2]int]bool{}
	groupOf := map[int]int{}
	for gi, g := range groups {
		for _, id := range g {
			groupOf[id] = gi
		}
	}
	for a := range h.nodes {
		for b := range h.nodes {
			if a != b && groupOf[a] != groupOf[b] {
				h.blocked[[2]int{a, b}] = true
			}
		}
	}
}

// Heal removes all partitions.
func (h *Hub) Heal() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blocked = map[[2]int]bool{}
}

// Cluster is a test/demo harness bundling a hub with its nodes.
type Cluster struct {
	Hub   *Hub
	Nodes []*Node
}

// NewCluster builds `size` nodes (ids 1..size). cfg may be nil for
// defaults (5s lease, snapshots off).
func NewCluster(size int, cfg func(id int) Config) *Cluster {
	hub := NewHub()
	c := &Cluster{Hub: hub}
	for i := 1; i <= size; i++ {
		peers := make([]int, 0, size-1)
		for j := 1; j <= size; j++ {
			if j != i {
				peers = append(peers, j)
			}
		}
		conf := Config{Lease: defaultLease}
		if cfg != nil {
			conf = cfg(i)
		}
		n, err := NewNode(i, peers, hub, conf)
		if err != nil {
			panic(err)
		}
		hub.Attach(n)
		c.Nodes = append(c.Nodes, n)
	}
	return c
}

func (c *Cluster) Node(id int) *Node { return c.Nodes[id-1] }

// SyncAll makes the leader bring every peer up to date.
func (c *Cluster) SyncAll(leader *Node) {
	for _, n := range c.Nodes {
		if n != leader {
			leader.SyncTo(n.ID())
		}
	}
}
