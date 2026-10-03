package paxos

import (
	"sort"
	"sync"
	"time"
)

// Network is a synchronous in-memory transport with partition control and
// message instrumentation. RPCs are delivered by direct method calls, so
// behaviour is fully deterministic under test.
type Network struct {
	mu      sync.Mutex
	nodes   map[int]*Node
	blocked map[[2]int]bool

	// Instrumentation (reset via ResetStats).
	PrepareCalls   int
	AcceptCalls    int
	CommitCalls    int
	HeartbeatCalls int
}

func newNetwork() *Network {
	return &Network{
		nodes:   make(map[int]*Node),
		blocked: make(map[[2]int]bool),
	}
}

// ResetStats zeroes the message counters.
func (n *Network) ResetStats() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.PrepareCalls, n.AcceptCalls, n.CommitCalls, n.HeartbeatCalls = 0, 0, 0, 0
}

// Stats returns a snapshot of the message counters.
func (n *Network) Stats() (prepare, accept, commit, heartbeat int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.PrepareCalls, n.AcceptCalls, n.CommitCalls, n.HeartbeatCalls
}

// Block drops all traffic from->to (directed).
func (n *Network) Block(from, to int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked[[2]int{from, to}] = true
}

// Partition splits the cluster: nodes in different groups cannot
// communicate; unlisted nodes become fully isolated.
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
			if a == b {
				continue
			}
			ga, oka := groupOf[a]
			gb, okb := groupOf[b]
			if !oka || !okb || ga != gb {
				n.blocked[[2]int{a, b}] = true
			}
		}
	}
}

// Heal removes all partitions.
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.blocked = make(map[[2]int]bool)
}

// targets returns reachable peers of `from`, sorted by id for determinism.
func (n *Network) targets(from int) []*Node {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []*Node
	for id, nd := range n.nodes {
		if id == from || n.blocked[[2]int{from, id}] {
			continue
		}
		out = append(out, nd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (n *Network) prepare(from int, b Ballot, fromIndex uint64) []Promise {
	targets := n.targets(from)
	n.mu.Lock()
	n.PrepareCalls += len(targets)
	n.mu.Unlock()
	out := make([]Promise, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.handlePrepare(b, fromIndex))
	}
	return out
}

func (n *Network) accept(from int, e Entry) []AcceptReply {
	targets := n.targets(from)
	n.mu.Lock()
	n.AcceptCalls += len(targets)
	n.mu.Unlock()
	out := make([]AcceptReply, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.handleAccept(e))
	}
	return out
}

func (n *Network) commit(from int, ballot Ballot) {
	leader := n.nodes[from]
	targets := n.targets(from)
	n.mu.Lock()
	n.CommitCalls += len(targets)
	n.mu.Unlock()
	for _, t := range targets {
		ci := t.CommitIndex()
		entries := leader.committedSince(ci)
		t.handleCommit(CommitMsg{
			Ballot:      ballot,
			LeaderID:    from,
			Entries:     entries,
			CommitIndex: leader.CommitIndex(),
		})
	}
}

// heartbeat sends a lease-granting heartbeat to each reachable peer and
// returns how many peers were reached.
func (n *Network) heartbeat(from int, ballot Ballot) int {
	leader := n.nodes[from]
	targets := n.targets(from)
	n.mu.Lock()
	n.HeartbeatCalls += len(targets)
	n.mu.Unlock()
	for _, t := range targets {
		ci := t.CommitIndex()
		entries := leader.committedSince(ci)
		t.handleHeartbeat(HeartbeatMsg{
			Ballot:      ballot,
			LeaderID:    from,
			Entries:     entries,
			CommitIndex: leader.CommitIndex(),
		})
	}
	return len(targets)
}

// Clock is a manually advanced clock for deterministic lease tests.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// NewClock starts a clock at the given instant.
func NewClock(start time.Time) *Clock { return &Clock{t: start} }

// Now returns the current clock time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// Cluster is a test/demo harness bundling nodes, network and clock.
type Cluster struct {
	Net   *Network
	Nodes []*Node
	Clock *Clock
}

// NewCluster builds a cluster of `size` nodes (ids 0..size-1) sharing a
// manual clock and the given lease duration.
func NewCluster(size int, lease time.Duration) *Cluster {
	clk := NewClock(time.Unix(1600000000, 0))
	net := newNetwork()
	c := &Cluster{Net: net, Clock: clk}
	for i := 0; i < size; i++ {
		nd := newNode(i, size, net, clk.Now, lease)
		net.nodes[i] = nd
		c.Nodes = append(c.Nodes, nd)
	}
	return c
}

// Partition delegates to the network.
func (c *Cluster) Partition(groups ...[]int) { c.Net.Partition(groups...) }

// Heal removes all partitions.
func (c *Cluster) Heal() { c.Net.Heal() }

// Advance moves the shared clock forward.
func (c *Cluster) Advance(d time.Duration) { c.Clock.Advance(d) }
