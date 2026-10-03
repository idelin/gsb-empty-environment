package paxos

import "sync/atomic"

// Transport is an in-memory, synchronous RPC fabric between nodes. Message
// delivery is a direct method call, which keeps tests fully deterministic.
// Connectivity is expressed as an atomic link matrix so partitions can be
// flipped without any lock on the data path.
type Transport struct {
	nodes []*Node
	links [][]atomic.Bool // links[i][j]: i can reach j

	// Message counters, exported for tests/demos (e.g. verifying that the
	// leader lease bypasses Phase 1).
	Prepares atomic.Uint64
	Accepts  atomic.Uint64
	Commits  atomic.Uint64
}

// NewTransport creates a fully-connected transport for n nodes.
func NewTransport(n int) *Transport {
	t := &Transport{
		nodes: make([]*Node, n),
		links: make([][]atomic.Bool, n),
	}
	for i := range t.links {
		t.links[i] = make([]atomic.Bool, n)
		for j := range t.links[i] {
			t.links[i][j].Store(true)
		}
	}
	return t
}

func (t *Transport) attach(id int, n *Node) { t.nodes[id] = n }

// SetPartition isolates the given groups from each other; nodes inside a
// group remain fully connected. Links are symmetric.
func (t *Transport) SetPartition(groups ...[]int) {
	inGroup := make(map[int]int)
	for gi, g := range groups {
		for _, id := range g {
			inGroup[id] = gi
		}
	}
	for i := range t.links {
		for j := range t.links[i] {
			gi, okI := inGroup[i]
			gj, okJ := inGroup[j]
			connected := i == j || !okI || !okJ || gi == gj
			t.links[i][j].Store(connected)
		}
	}
}

// Heal restores full connectivity.
func (t *Transport) Heal() {
	for i := range t.links {
		for j := range t.links[i] {
			t.links[i][j].Store(true)
		}
	}
}

func (t *Transport) connected(from, to int) bool {
	return t.links[from][to].Load()
}

// Prepare delivers a Phase 1a request; ok=false means the link is down.
func (t *Transport) Prepare(from, to int, m PrepareMsg) (PromiseMsg, bool) {
	t.Prepares.Add(1)
	if !t.connected(from, to) {
		return PromiseMsg{}, false
	}
	return t.nodes[to].handlePrepare(m), true
}

// Accept delivers a Phase 2a request; ok=false means the link is down.
func (t *Transport) Accept(from, to int, m AcceptMsg) (AcceptedMsg, bool) {
	t.Accepts.Add(1)
	if !t.connected(from, to) {
		return AcceptedMsg{}, false
	}
	return t.nodes[to].handleAccept(m), true
}

// Commit delivers a commit notification; ok=false means the link is down.
func (t *Transport) Commit(from, to int, m CommitMsg) bool {
	t.Commits.Add(1)
	if !t.connected(from, to) {
		return false
	}
	t.nodes[to].handleCommit(m)
	return true
}
