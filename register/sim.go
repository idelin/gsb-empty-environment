package register

import (
	"container/heap"
	"math/rand"
)

// event is a single scheduled action at a virtual timestamp.
type event struct {
	at  int64
	seq int
	fn  func()
}

// eventHeap is a priority queue ordered by (time, insertion sequence),
// which makes the simulation deterministic for a given seed.
type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) {
	*h = append(*h, x.(event))
}
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Sim is a single-process discrete-event simulator. It controls all
// communication (message delays and reordering), failures (crash and
// restart of nodes) and time (a virtual clock advanced event by event).
type Sim struct {
	// Now is the current virtual time.
	Now int64

	// MinDelay and MaxDelay bound the random one-way network delay.
	MinDelay int64
	MaxDelay int64

	queue  eventHeap
	seq    int
	rnd    *rand.Rand
	nodes  []*Node
	quorum int

	nextClientID int
}

// NewSim builds a cluster of numNodes replicas. With n nodes the quorum
// size is n/2+1 and the cluster tolerates f = (n-1)/2 crashed nodes.
func NewSim(numNodes int, seed int64) *Sim {
	s := &Sim{
		rnd:      rand.New(rand.NewSource(seed)),
		MinDelay: 1,
		MaxDelay: 5,
	}
	for i := 0; i < numNodes; i++ {
		s.nodes = append(s.nodes, &Node{id: i, sim: s, up: true})
	}
	s.quorum = numNodes/2 + 1
	return s
}

// NumNodes returns the cluster size n.
func (s *Sim) NumNodes() int { return len(s.nodes) }

// Quorum returns the majority size n/2+1 required by every operation.
func (s *Sim) Quorum() int { return s.quorum }

// FaultTolerance returns f = n - quorum = (n-1)/2, the number of node
// crashes the register tolerates while remaining available.
func (s *Sim) FaultTolerance() int { return len(s.nodes) - s.quorum }

// Nodes returns the replicas, indexed by node ID.
func (s *Sim) Nodes() []*Node { return s.nodes }

// NewClient creates a client with a cluster-wide unique ID. The ID is
// used as the tie-breaker in version tags.
func (s *Sim) NewClient() *Client {
	s.nextClientID++
	return &Client{id: s.nextClientID, sim: s}
}

// After schedules fn to run d virtual time units from now.
func (s *Sim) After(d int64, fn func()) {
	heap.Push(&s.queue, event{at: s.Now + d, seq: s.seq, fn: fn})
	s.seq++
}

// Step executes the next event. It returns false when no events remain.
func (s *Sim) Step() bool {
	if len(s.queue) == 0 {
		return false
	}
	e := heap.Pop(&s.queue).(event)
	s.Now = e.at
	e.fn()
	return true
}

// Run drains the event queue.
func (s *Sim) Run() {
	for s.Step() {
	}
}

// RunUntil runs events until cond holds or the queue is empty.
func (s *Sim) RunUntil(cond func() bool) {
	for !cond() && s.Step() {
	}
}

// netDelay draws a random one-way network delay.
func (s *Sim) netDelay() int64 {
	d := s.MinDelay
	if s.MaxDelay > s.MinDelay {
		d += s.rnd.Int63n(s.MaxDelay - s.MinDelay + 1)
	}
	return d
}

// sendRequest hands a request to the network. Delivery is delayed by a
// random amount, so concurrent messages may be reordered. Every request
// eventually produces exactly one reply: a positive reply if the node is
// up at delivery time, a negative one (node crashed) otherwise.
func (s *Sim) sendRequest(to *Node, r request) {
	s.After(s.netDelay(), func() { to.deliver(r) })
}

// sendReply hands a reply to the network.
func (s *Sim) sendReply(fn func()) {
	s.After(s.netDelay(), fn)
}
